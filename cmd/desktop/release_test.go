package main

import (
	"testing"

	"github.com/tiaot33/mechbox/cmd/desktop/internal/release"
)

// release 包给出的每一种「只能从下载页装」的原因，都对应前端认得的一种，取值相同。
func TestManualReasonCoversEveryReleaseReason(t *testing.T) {
	for reason, want := range map[release.ManualReason]ManualReason{
		release.ManualPackage:  ManualPackage,
		release.ManualLocation: ManualLocation,
		release.ManualAsset:    ManualAsset,
		"":                     "",
	} {
		if got := manualOf(reason); got != want || string(got) != string(reason) {
			t.Errorf("manualOf(%q) = %q; want %q", reason, got, want)
		}
	}
}
