package buildinfo

import "testing"

func TestDefaultInfoMatchesPackageLevel(t *testing.T) {
	i := Default()
	if i.String() != BuildInfo() {
		t.Errorf("Info.String() = %q, want %q", i.String(), BuildInfo())
	}
	if i.FullVersion() != FullVersion() || i.UserAgent() != UserAgent() || i.IsDev() != IsDev() {
		t.Error("Info methods disagree with the package-level functions")
	}
}
