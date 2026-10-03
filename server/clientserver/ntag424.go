package clientserver

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// NTAG 424 operations a client may name.
const (
	ntag424GetFileSettings = "getFileSettings"
	ntag424ConfigureSDM    = "configureSDM"
	ntag424ChangeKey       = "changeKey"
	ntag424GetCardUID      = "getCardUID"
	ntag424GetKeyVersion   = "getKeyVersion"
	ntag424ReadSig         = "readSig"
	ntag424Lock            = "lock"
)

const (
	keySourceConfigured = "configured"
	keySourceExplicit   = "explicit"
)

func ntag424Mutating(op string) bool {
	return op == ntag424ConfigureSDM || op == ntag424ChangeKey || op == ntag424Lock
}

// NTAG424 runs an NTAG 424 DNA operation on the named tag. Operations that
// change the tag are refused in read-only mode, as a write is; the rest are
// reads. None needs the raw channel, since the agent builds every command
// itself.
func (s *tagOps) NTAG424(ctx context.Context, req server.NTAG424Op) (*protocol.NTAG424ResponsePayload, error) {
	r := req.Request
	switch r.Op {
	case ntag424GetFileSettings, ntag424ConfigureSDM, ntag424ChangeKey, ntag424GetCardUID,
		ntag424GetKeyVersion, ntag424ReadSig, ntag424Lock:
	default:
		return nil, protocol.Errorf(protocol.ErrCodeInvalidRequest, "unknown ntag424 op %q", r.Op)
	}

	mutating := ntag424Mutating(r.Op)
	if mutating && !s.modificationAllowed() {
		return nil, protocol.Errorf(protocol.ErrCodeReadOnly, "%s", readOnlyModeMessage("NTAG 424 changes"))
	}

	run, detail, err := s.ntag424Plan(r)
	if err != nil {
		return nil, err
	}

	holder, ok := s.tags.(nfc.NTAG424Holder)
	if !ok {
		return nil, protocol.WrapError(protocol.ErrCodeNotSupported, nfc.NewNotSupportedError("NTAG424"),
			"NTAG 424 operations are not supported here")
	}

	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	// Recorded before the operation, as a raw exchange is, with the numbers it
	// names and never a key.
	auditNTAG424(r.Op, mutating, rt.device, rt.uid, detail)

	var out *protocol.NTAG424ResponsePayload
	err = holder.WithNTAG424Tag(ctx, rt.device, rt.uid, func(op nfc.NTAG424Operator) error {
		var err error
		out, err = run(op)
		return err
	})
	if err != nil {
		return nil, ntag424Failure(err, rt.device, r.Op)
	}
	return out, nil
}

// ntag424Plan validates a request and returns what runs it, with a description
// for the audit log that names no key material.
func (s *tagOps) ntag424Plan(r protocol.NTAG424RequestPayload) (func(nfc.NTAG424Operator) (*protocol.NTAG424ResponsePayload, error), string, error) {
	invalid := func(format string, args ...any) error {
		return protocol.Errorf(protocol.ErrCodeInvalidRequest, format, args...)
	}
	out := func() *protocol.NTAG424ResponsePayload { return &protocol.NTAG424ResponsePayload{Op: r.Op} }

	switch r.Op {
	case ntag424GetFileSettings:
		fileNo := r.FileNo
		if fileNo == 0 {
			fileNo = ntag424.NDEFFileNo
		}
		if fileNo < 1 || fileNo > 3 {
			return nil, "", invalid("fileNo must be 1 to 3")
		}
		return func(op nfc.NTAG424Operator) (*protocol.NTAG424ResponsePayload, error) {
			fs, err := op.GetFileSettings(byte(fileNo))
			if err != nil {
				return nil, err
			}
			o := out()
			o.FileSettings = fileSettingsPayload(fs)
			return o, nil
		}, fmt.Sprintf("file %d", fileNo), nil

	case ntag424ConfigureSDM:
		if strings.TrimSpace(r.URLTemplate) == "" {
			return nil, "", invalid("urlTemplate is required")
		}
		opts, err := sdmOptions(r.SDM)
		if err != nil {
			return nil, "", invalid("%v", err)
		}
		plan, err := ntag424.PlanSDM(r.URLTemplate, opts)
		if err != nil {
			return nil, "", invalid("%v", err)
		}
		return func(op nfc.NTAG424Operator) (*protocol.NTAG424ResponsePayload, error) {
			res, err := op.ConfigureSDM(plan)
			if res == nil {
				return nil, err
			}
			o := out()
			o.SDM = &protocol.NTAG424SDMResult{URL: res.URL, Verified: res.Tap != nil}
			if res.Tap != nil {
				o.SDM.UID = res.Tap.UIDString()
				o.SDM.Counter = res.Tap.ReadCounter
			}
			if err != nil {
				o.SDM.VerifyError = err.Error()
			}
			return o, nil
		}, "configure SDM", nil

	case ntag424ChangeKey:
		if !r.Confirm {
			return nil, "", invalid("changeKey is irreversible; set confirm to true")
		}
		if r.KeyNo < 0 || r.KeyNo > 4 || r.AuthKeyNo < 0 || r.AuthKeyNo > 4 {
			return nil, "", invalid("keyNo and authKeyNo must be 0 to 4")
		}
		if r.Version < 0 || r.Version > 0xFF {
			return nil, "", invalid("version must be 0 to 255")
		}
		var explicit []byte
		switch r.NewKeySource {
		case keySourceConfigured:
			if r.NewKey != "" {
				return nil, "", invalid("newKey is for the explicit key source only")
			}
		case keySourceExplicit:
			var err error
			if explicit, err = hex.DecodeString(r.NewKey); err != nil || len(explicit) != ntag424.KeySize {
				return nil, "", invalid("newKey must be 32 hex characters")
			}
		default:
			return nil, "", invalid(`newKeySource must be "configured" or "explicit"`)
		}
		detail := fmt.Sprintf("key %d (auth key %d, version %d, %s key)", r.KeyNo, r.AuthKeyNo, r.Version, r.NewKeySource)
		return func(op nfc.NTAG424Operator) (*protocol.NTAG424ResponsePayload, error) {
			newKey := explicit
			if newKey == nil {
				source, ok := op.(nfc.NTAG424KeySource)
				if !ok {
					return nil, nfc.NewNotSupportedError("ConfiguredKey")
				}
				var err error
				if newKey, err = source.ConfiguredKey(byte(r.KeyNo)); err != nil {
					return nil, err
				}
			}
			if err := op.ChangeKey(byte(r.KeyNo), newKey, byte(r.Version), byte(r.AuthKeyNo)); err != nil {
				return nil, err
			}
			o := out()
			o.Changed = true
			return o, nil
		}, detail, nil

	case ntag424GetCardUID:
		return func(op nfc.NTAG424Operator) (*protocol.NTAG424ResponsePayload, error) {
			uid, err := op.GetCardUID()
			if err != nil {
				return nil, err
			}
			o := out()
			o.UID = nfc.BytesToHex(uid)
			return o, nil
		}, "", nil

	case ntag424GetKeyVersion:
		if r.KeyNo < 0 || r.KeyNo > 4 {
			return nil, "", invalid("keyNo must be 0 to 4")
		}
		return func(op nfc.NTAG424Operator) (*protocol.NTAG424ResponsePayload, error) {
			v, err := op.GetKeyVersion(byte(r.KeyNo))
			if err != nil {
				return nil, err
			}
			o := out()
			keyNo, version := r.KeyNo, int(v)
			o.KeyNo, o.KeyVersion = &keyNo, &version
			return o, nil
		}, fmt.Sprintf("key %d", r.KeyNo), nil

	case ntag424ReadSig:
		return func(op nfc.NTAG424Operator) (*protocol.NTAG424ResponsePayload, error) {
			sig, err := op.ReadSig()
			if err != nil {
				return nil, err
			}
			o := out()
			o.Signature = base64.StdEncoding.EncodeToString(sig)
			return o, nil
		}, "", nil

	default:
		if !r.Confirm {
			return nil, "", invalid("lock is irreversible; set confirm to true")
		}
		return func(op nfc.NTAG424Operator) (*protocol.NTAG424ResponsePayload, error) {
			locker, ok := op.(nfc.TagLocker)
			if !ok {
				return nil, nfc.NewNotSupportedError("MakeReadOnly")
			}
			if err := locker.MakeReadOnly(); err != nil {
				return nil, err
			}
			o := out()
			o.Locked = true
			return o, nil
		}, "make the NDEF file read-only", nil
	}
}

// sdmOptions fills the defaults for options a request left out and checks the
// numbers.
func sdmOptions(in *protocol.NTAG424SDMOptions) (ntag424.SDMOptions, error) {
	if in == nil {
		in = &protocol.NTAG424SDMOptions{}
	}
	var bad error
	pick := func(name string, v *int, def byte) byte {
		if v == nil {
			return def
		}
		if n := *v; (n < 0 || n > 4) && n != ntag424.AccessFree && n != ntag424.AccessNever {
			bad = fmt.Errorf("%s %d is not a key number 0 to 4, 14 or 15", name, n)
		}
		return byte(*v)
	}
	opts := ntag424.SDMOptions{
		MetaRead:   pick("metaRead", in.MetaRead, 0),
		FileRead:   pick("fileRead", in.FileRead, 0),
		CounterRet: pick("counterRet", in.CounterRet, ntag424.AccessNever),
		Change:     pick("change", in.Change, 0),
		Read:       pick("read", in.Read, ntag424.AccessFree),
		Write:      pick("write", in.Write, 0),
		ReadWrite:  pick("readWrite", in.ReadWrite, 0),
	}
	if in.EncLength < 0 {
		return opts, fmt.Errorf("encLength must not be negative")
	}
	opts.EncLength = uint32(in.EncLength)
	return opts, bad
}

func fileSettingsPayload(fs *ntag424.FileSettings) *protocol.NTAG424FileSettings {
	mode := "plain"
	switch fs.CommMode {
	case ntag424.CommMAC:
		mode = "mac"
	case ntag424.CommFull:
		mode = "full"
	}
	return &protocol.NTAG424FileSettings{
		FileType:          int(fs.FileType),
		FileSize:          fs.FileSize,
		CommMode:          mode,
		ReadWrite:         int(fs.ReadWrite),
		Change:            int(fs.Change),
		Read:              int(fs.Read),
		Write:             int(fs.Write),
		SDMEnabled:        fs.SDMEnabled,
		MirrorUID:         fs.MirrorUID,
		MirrorReadCounter: fs.MirrorReadCounter,
		ReadCounterLimit:  fs.ReadCounterLimit,
		EncryptFileData:   fs.EncryptFileData,
		ASCIIEncoding:     fs.ASCIIEncoding,
		SDMMetaRead:       int(fs.SDMMetaRead),
		SDMFileRead:       int(fs.SDMFileRead),
		SDMCounterRet:     int(fs.SDMCounterRet),
		UIDOffset:         fs.UIDOffset,
		ReadCounterOffset: fs.ReadCounterOffset,
		PICCDataOffset:    fs.PICCDataOffset,
		MACInputOffset:    fs.MACInputOffset,
		MACOffset:         fs.MACOffset,
		ENCOffset:         fs.ENCOffset,
		ENCLength:         fs.ENCLength,
	}
}

// ntag424Failure reports a failure of an NTAG 424 operation in the codes a
// client knows. A refusal by the card for want of a key or a permission is an
// authentication failure; any other status the card answers is a failed
// exchange.
func ntag424Failure(err error, device, op string) error {
	var coded *protocol.CodedError
	var nfcErr *nfc.NFCError
	if errors.As(err, &coded) || errors.As(err, &nfcErr) {
		return sourceFailure(err, device, "NTAG 424 "+op, protocol.ErrCodeTransceiveFailed)
	}
	switch {
	case errors.Is(err, ntag424.ErrPermissionDenied), errors.Is(err, ntag424.ErrAuthDelay),
		errors.Is(err, ntag424.ErrLRP), errors.Is(err, ntag424.ErrAuthFailed):
		return protocol.WrapError(protocol.ErrCodeAuthFailed, err, "NTAG 424 %s was refused", op)
	}
	return protocol.WrapError(protocol.ErrCodeTransceiveFailed, err, "NTAG 424 %s failed", op)
}
