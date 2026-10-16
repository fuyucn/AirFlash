package i18n

import "testing"

func TestChineseIsSelectedFromAPreference(t *testing.T) {
	// A machine reports its languages best first, as "zh-CN" or "en-US". The part
	// before the dash is the language.
	for _, pref := range []string{"zh", "zh-CN", "zh_TW", "ZH-Hans"} {
		if got := Select(pref); got != Chinese {
			t.Errorf("Select(%q) = %q, want Chinese", pref, got)
		}
	}
	for _, pref := range []string{"en", "en-US", "", "fr"} {
		if got := Select(pref); got != English {
			t.Errorf("Select(%q) = %q, want English", pref, got)
		}
	}
}

func TestATranslationFallsBackToItsEnglishKey(t *testing.T) {
	// A string that has no translation yet shows its English key rather than
	// nothing: a translation that falls behind degrades one string at a time,
	// which is the difference between an odd line and a blank screen.
	tru := For(Chinese)
	if got := tru.T("Lightweight mode"); got != "轻量模式" {
		t.Errorf("a translated key = %q, want its Chinese", got)
	}
	if got := tru.T("A string nobody has translated yet"); got != "A string nobody has translated yet" {
		t.Errorf("an untranslated key = %q, want the English key", got)
	}
}

func TestEnglishNeedsNoTable(t *testing.T) {
	// Every key is already English, so the English translator has nothing to hold:
	// this is why the language can be added to the interface without touching a
	// single call site.
	tru := For(English)
	if got := tru.T("anything"); got != "anything" {
		t.Errorf("English translated %q to %q, want it unchanged", "anything", got)
	}
}

func TestSystemResolvesToALanguageTheTableKnows(t *testing.T) {
	// Everything downstream compares against English or Chinese, so System has to
	// become one of them here: carrying it further would mean checking for it at
	// every call site, and a missed check is a blank line in the interface.
	lang := FromEnvironment()
	if lang != English && lang != Chinese {
		t.Errorf("system resolved to %q, want English or Chinese", lang)
	}
	if words := For(System).words; words != nil && len(words) == 0 {
		t.Error("System resolved to a language with an empty table, so every string would be its own key")
	}
}

func TestTheChineseTableNamesAHomePodLikeTheDesktopDoes(t *testing.T) {
	// A few of these are the desktop's own words, and a person who has used that
	// recognises them here. Spotting them keeps the two hosts from describing the
	// same thing differently.
	tru := For(Chinese)
	for _, key := range []string{"Playing", "Connecting", "Custom"} {
		if got := tru.T(key); got == key {
			t.Errorf("%q has no Chinese, but the desktop names it", key)
		}
	}
	if got := tru.T("Playing"); got != "正在播放" {
		t.Errorf("Playing = %q, want the desktop's 正在播放", got)
	}
}
