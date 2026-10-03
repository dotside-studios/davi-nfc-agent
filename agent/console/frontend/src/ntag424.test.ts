import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import type { NTAG424Request, NTAG424Response, NTAG424SDMPlan } from '@davi/nfc-agent-client'
import { describe, expect, it } from 'vitest'
import {
  DEFAULT_CHANGE_KEY_FORM,
  DEFAULT_SDM_FORM,
  checkChangeKey,
  confirmedChangeKey,
  confirmedLock,
  describeFileSettings,
  layoutRows,
  looksLikeNtag424,
  phraseMatches,
  planRegions,
  previewPlan,
  readCard,
  rightLabel,
  sdmRequest,
  signatureHex,
} from './ntag424'

/**
 * The plans are what the agent's planSDM answers, written by a Go test
 * (server/clientserver TestNTAG424PlanContract) and read here, so the layout
 * code is held to the real planner rather than to numbers copied by hand.
 */
interface Case {
  name: string
  request: { urlTemplate: string }
  response: { plan: NTAG424SDMPlan }
}

const fixtureURL = new URL('../../../../server/clientserver/testdata/ntag424_plan_cases.json', import.meta.url)
const cases = JSON.parse(readFileSync(fileURLToPath(fixtureURL), 'utf8')) as Case[]

function planOf(name: string): { plan: NTAG424SDMPlan; template: string } {
  const c = cases.find((x) => x.name === name)
  if (!c) throw new Error(`no fixture case ${name}`)
  return { plan: c.response.plan, template: c.request.urlTemplate }
}

describe('the plan preview against the agent planner', () => {
  it('reads a non-empty fixture', () => {
    expect(cases.length).toBeGreaterThan(0)
  })

  for (const c of cases) {
    it(`${c.name}: the URL is the template with zeros for each mirror`, () => {
      const preview = previewPlan(c.response.plan)
      expect(preview).not.toBeNull()
      const expected = c.request.urlTemplate
        .replace('{picc}', '0'.repeat(32))
        .replace('{uid}', '0'.repeat(14))
        .replace('{ctr}', '0'.repeat(6))
        .replace('{enc}', '0'.repeat(c.response.plan.settings?.encLength ?? 32))
        .replace('{mac}', '0'.repeat(16))
      expect(preview?.url).toBe(expected)
      expect(preview?.length).toBe(c.response.plan.length)
      expect(preview?.fits).toBe(true)
    })

    it(`${c.name}: every mirror region holds exactly the zeros of its width`, () => {
      const preview = previewPlan(c.response.plan)
      const regions = preview?.segments.filter((s) => s.kind) ?? []
      expect(regions.length).toBeGreaterThan(0)
      for (const s of regions) expect(s.text).toMatch(/^0+$/)
    })
  }

  it('marks the PICC data and the MAC at the offsets the settings give', () => {
    const { plan } = planOf('picc and mac')
    const regions = planRegions(plan)
    expect(regions.map((r) => r.kind)).toEqual(['picc', 'mac'])
    expect(regions[0].end - regions[0].start).toBe(32)
    expect(regions[1].end - regions[1].start).toBe(16)

    const preview = previewPlan(plan)
    const kinds = preview?.segments.map((s) => s.kind)
    expect(kinds).toEqual([undefined, 'picc', undefined, 'mac'])
    expect(preview?.prefix).toBe('https://')
  })

  it('marks uid and counter separately and in the clear', () => {
    const { plan } = planOf('uid and counter in the clear')
    expect(planRegions(plan).map((r) => [r.kind, r.end - r.start])).toEqual([
      ['uid', 14],
      ['ctr', 6],
      ['mac', 16],
    ])
  })

  it('marks the span the MAC covers only when file data is encrypted', () => {
    const plain = previewPlan(planOf('picc and mac').plan)
    expect(plain?.segments.some((s) => s.macInput)).toBe(false)

    const enc = previewPlan(planOf('encrypted file data').plan)
    const covered = enc?.segments.filter((s) => s.macInput) ?? []
    expect(covered.map((s) => s.kind)).toEqual(['enc', undefined])
    expect(covered[0].text).toBe('0'.repeat(64))
  })

  it('reads the prefix abbreviation, including none', () => {
    expect(previewPlan(planOf('http with www prefix').plan)?.prefix).toBe('http://www.')
    expect(previewPlan(planOf('no prefix abbreviation').plan)?.prefix).toBe('')
  })

  it('lists the offsets for the table', () => {
    const rows = layoutRows(planOf('picc and mac').plan)
    expect(rows.map((r) => r.label)).toEqual(['PICC data', 'MAC'])
    expect(rows[0].offset).toBe(planOf('picc and mac').plan.settings?.piccDataOffset)
  })

  it('refuses content that is not one URI record', () => {
    const { plan } = planOf('picc and mac')
    expect(previewPlan({ ...plan, ndefHex: '0000' })).toBeNull()
    expect(previewPlan({ ...plan, ndefHex: 'zz' })).toBeNull()
    expect(previewPlan({ ...plan, ndefHex: plan.ndefHex.replace('D1014455', 'D1014454') })).toBeNull()
  })

  it('reports a plan larger than the file', () => {
    const { plan } = planOf('picc and mac')
    expect(previewPlan({ ...plan, length: 300 })?.fits).toBe(false)
  })
})

describe('sdmRequest', () => {
  it('builds the planner request from the form, defaults included', () => {
    const built = sdmRequest(DEFAULT_SDM_FORM, 'planSDM')
    expect(built).toEqual({
      ok: true,
      request: {
        op: 'planSDM',
        urlTemplate: DEFAULT_SDM_FORM.template,
        sdm: { metaRead: 0, fileRead: 0, counterRet: 15, change: 0, read: 14, write: 0, readWrite: 0 },
      },
    })
  })

  it('sends encLength only when the template mirrors encrypted data', () => {
    const without = sdmRequest(DEFAULT_SDM_FORM, 'configureSDM')
    expect(without.ok && 'encLength' in (without.request.sdm ?? {})).toBe(false)

    const withEnc = sdmRequest(
      { ...DEFAULT_SDM_FORM, template: 'https://x.test/?p={picc}&e={enc}&m={mac}', encLength: '64' },
      'configureSDM',
    )
    expect(withEnc.ok && withEnc.request.sdm?.encLength).toBe(64)
  })

  const bad: [string, Partial<typeof DEFAULT_SDM_FORM>, RegExp][] = [
    ['empty', { template: '  ' }, /template/],
    ['no mac', { template: 'https://x.test/?p={picc}' }, /\{mac\}/],
    ['picc with uid', { template: 'https://x.test/{picc}{uid}{ctr}{mac}' }, /cannot be combined/],
    ['uid without ctr', { template: 'https://x.test/{uid}{mac}' }, /go together/],
    ['no mirror', { template: 'https://x.test/{mac}' }, /needs \{picc\}/],
    ['file read is free', { fileRead: 14 }, /key 0 to 4/],
    ['enc width', { template: 'https://x.test/{picc}{enc}{mac}', encLength: '40' }, /multiple of 32/],
  ]
  for (const [name, patch, message] of bad) {
    it(`refuses ${name}`, () => {
      const built = sdmRequest({ ...DEFAULT_SDM_FORM, ...patch }, 'planSDM')
      expect(built.ok).toBe(false)
      expect(!built.ok && built.error).toMatch(message)
    })
  }
})

describe('change key and lock confirmation', () => {
  it('builds no request until the phrase is typed', () => {
    expect(() => confirmedChangeKey(DEFAULT_CHANGE_KEY_FORM, '')).toThrow(/phrase/)
    expect(() => confirmedChangeKey(DEFAULT_CHANGE_KEY_FORM, 'change key 2')).toThrow(/phrase/)
    expect(() => confirmedLock('')).toThrow(/phrase/)
    expect(() => confirmedLock('lock it')).toThrow(/phrase/)
  })

  it('sends confirm true, and only then', () => {
    expect(confirmedChangeKey(DEFAULT_CHANGE_KEY_FORM, ' Change Key 1 ')).toEqual({
      op: 'changeKey',
      keyNo: 1,
      authKeyNo: 0,
      version: 0,
      newKeySource: 'configured',
      confirm: true,
    })
    expect(confirmedLock('lock')).toEqual({ op: 'lock', confirm: true })
  })

  it('carries an explicit key only for the explicit source', () => {
    const key = '00112233445566778899AABBCCDDEEFF'
    const req = confirmedChangeKey(
      { ...DEFAULT_CHANGE_KEY_FORM, keyNo: '3', version: '7', source: 'explicit', newKey: key.toLowerCase().replace(/(..)/g, '$1 ').trim() },
      'change key 3',
    )
    expect(req).toMatchObject({ keyNo: 3, version: 7, newKeySource: 'explicit', newKey: key.toLowerCase() })

    const configured = confirmedChangeKey({ ...DEFAULT_CHANGE_KEY_FORM, newKey: key }, 'change key 1')
    expect('newKey' in configured).toBe(false)
  })

  const bad: [string, Partial<typeof DEFAULT_CHANGE_KEY_FORM>, RegExp][] = [
    ['key number out of range', { keyNo: '5' }, /Key number/],
    ['key number blank', { keyNo: '' }, /Key number/],
    ['authenticating key out of range', { authKeyNo: '-1' }, /Authenticating/],
    ['version out of range', { version: '256' }, /Version/],
    ['short explicit key', { source: 'explicit', newKey: 'abcd' }, /32 hex/],
    ['non-hex explicit key', { source: 'explicit', newKey: 'zz'.repeat(16) }, /32 hex/],
  ]
  for (const [name, patch, message] of bad) {
    it(`refuses ${name}`, () => {
      const checked = checkChangeKey({ ...DEFAULT_CHANGE_KEY_FORM, ...patch })
      expect(checked.ok).toBe(false)
      expect(!checked.ok && checked.error).toMatch(message)
    })
  }

  it('matches phrases without regard to case or padding', () => {
    expect(phraseMatches('  LOCK ', 'lock')).toBe(true)
    expect(phraseMatches('loc', 'lock')).toBe(false)
  })
})

describe('readCard', () => {
  it('asks for each part and keeps going past one the card refuses', async () => {
    const asked: string[] = []
    const run = async (r: NTAG424Request): Promise<NTAG424Response> => {
      asked.push(r.op === 'getKeyVersion' ? `${r.op}:${r.keyNo}` : r.op)
      switch (r.op) {
        case 'getFileSettings':
          return { op: r.op, fileSettings: planOf('picc and mac').plan.settings }
        case 'getCardUID':
          throw new Error('authentication was refused')
        case 'getKeyVersion':
          return { op: r.op, keyNo: r.keyNo, keyVersion: r.keyNo * 2 }
        default:
          return { op: r.op, signature: btoa('\x01\xab') }
      }
    }

    const reading = await readCard(run)
    expect(asked).toEqual([
      'getFileSettings',
      'getCardUID',
      'getKeyVersion:0',
      'getKeyVersion:1',
      'getKeyVersion:2',
      'getKeyVersion:3',
      'getKeyVersion:4',
      'readSig',
    ])
    expect(reading.fileSettings.ok).toBe(true)
    expect(reading.uid).toEqual({ ok: false, error: 'authentication was refused' })
    expect(reading.keyVersions.map((k) => k.ok && k.value)).toEqual([0, 2, 4, 6, 8])
    expect(reading.signature).toEqual({ ok: true, value: btoa('\x01\xab') })
  })

  it('reports an answer missing its field instead of passing it off', async () => {
    const reading = await readCard(async (r) => ({ op: r.op }))
    expect(reading.uid.ok).toBe(false)
    expect(reading.fileSettings.ok).toBe(false)
  })
})

describe('display helpers', () => {
  it('names access rights', () => {
    expect(rightLabel(2)).toBe('key 2')
    expect(rightLabel(14)).toBe('free')
    expect(rightLabel(15)).toBe('never')
  })

  it('describes SDM settings only when SDM is on', () => {
    const settings = planOf('picc and mac').plan.settings
    if (!settings) throw new Error('fixture has no settings')

    const on = describeFileSettings(settings)
    expect(on.find((r) => r.label === 'Mirrors')?.value).toBe('UID, counter, MAC')
    expect(on.find((r) => r.label === 'Read')?.value).toBe('free')

    const off = describeFileSettings({ ...settings, sdmEnabled: false })
    expect(off.some((r) => r.label === 'Mirrors')).toBe(false)
  })

  it('recognises an NTAG 424 DNA by the type a reader or phone reports', () => {
    expect(looksLikeNtag424('NTAG 424 DNA')).toBe(true)
    expect(looksLikeNtag424('NTAG424')).toBe(true)
    expect(looksLikeNtag424('NTAG215')).toBe(false)
    expect(looksLikeNtag424('Type4')).toBe(false)
    expect(looksLikeNtag424(undefined)).toBe(false)
  })

  it('shows a signature as hex', () => {
    expect(signatureHex(btoa('\x01\xab'))).toBe('01AB')
    expect(signatureHex('!!')).toBe('')
  })
})
