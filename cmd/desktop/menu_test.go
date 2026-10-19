package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestNativeLabelsCoverBothLanguages(t *testing.T) {
	for key := range nativeLabels[LanguageZh] {
		if nativeLabels[LanguageEn][key] == "" {
			t.Errorf("label %q has no English text", key)
		}
	}
	for key := range nativeLabels[LanguageEn] {
		if nativeLabels[LanguageZh][key] == "" {
			t.Errorf("label %q has no Chinese text", key)
		}
	}
}

func TestNativeMenusFollowLanguage(t *testing.T) {
	var m nativeMenus
	m.setLanguage(LanguageZh)
	open := m.label(application.NewMenuItem(""), "open")
	if got := open.Label(); got != "打开 Mechbox" {
		t.Fatalf("label = %q; want the Chinese text", got)
	}
	m.setLanguage(LanguageEn)
	quit := m.label(application.NewMenuItem(""), "quit")
	if open.Label() != "Open Mechbox" || quit.Label() != "Quit Mechbox" {
		t.Fatalf("labels = %q, %q; want the English text", open.Label(), quit.Label())
	}
}
