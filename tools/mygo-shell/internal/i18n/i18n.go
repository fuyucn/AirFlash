// Package i18n holds the interface's words in the languages it speaks.
//
// English is the key, not a language: it is what the code reads when a string
// has not been translated, and it is what a machine with no language preference
// is shown. A translated string that is missing shows its English key rather
// than nothing, so a translation that falls behind the interface degrades to
// English one string at a time instead of blanking a screen.
package i18n

import (
	"os"
	"strings"
)

// Language is a choice of words.
type Language string

const (
	// System follows the machine's own preference, which is what most people
	// want and what the desktop host defaults to.
	System Language = "system"
	// English is the source of every key.
	English Language = "en"
	// Chinese is simplified.
	Chinese Language = "zh"
)

// Supported is what can be chosen, in the order the desktop offers them.
func Supported() []Language {
	return []Language{System, English, Chinese}
}

// Labels are what a language is called in itself, so a reader can find their own.
func Labels() map[Language]string {
	return map[Language]string{
		System:  "System default",
		English: "English",
		Chinese: "简体中文",
	}
}

// Select reduces a preference to a language it can speak.
//
// A machine reports its languages as a list, best first, as "zh-CN" or "en-US":
// the part before the dash is the language, and anything after it is a region
// that does not change which words are used here. An empty or unrecognised
// preference is English, which is what every key already is.
//
// The separator is either a dash or an underscore, and Windows uses both: a
// locale in the environment is "zh_CN" while a language tag elsewhere is
// "zh-CN".
func Select(preference string) Language {
	text := strings.TrimSpace(preference)
	if text == "" {
		return English
	}
	lang := strings.ToLower(strings.FieldsFunc(text, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})[0])
	switch lang {
	case "zh":
		return Chinese
	default:
		return English
	}
}

// FromEnvironment is the language the machine asks for, read once.
//
// Windows and macOS both put the preference in LANG, and Windows sets it in
// addition to its own language list, so one variable is enough on both.
func FromEnvironment() Language {
	return Select(os.Getenv("LANG"))
}

// Translator turns the interface's English keys into a language.
type Translator struct {
	// Lang is the language System resolved to, so a caller never has to handle a
	// value that is neither English nor Chinese.
	Lang  Language
	words map[string]string
}

// For returns the translator for a language.
//
// System is resolved here rather than carried around: everything after this
// point holds a language that is either English or Chinese, so no caller has to
// check for it.
func For(lang Language) Translator {
	switch lang {
	case Chinese:
		return Translator{Lang: Chinese, words: chinese}
	case System:
		resolved := FromEnvironment()
		return Translator{Lang: resolved, words: chineseFor(resolved)}
	default:
		return Translator{Lang: English, words: nil}
	}
}

// chineseFor is the Chinese table for a resolved language, or none for English.
func chineseFor(lang Language) map[string]string {
	if lang == Chinese {
		return chinese
	}
	return nil
}

// T is one translated string.
//
// The English key is passed rather than a symbol, because a symbol would have to
// be looked up in this file to know what it says, while the key is the sentence
// itself.
func (t Translator) T(english string) string {
	if t.words == nil {
		return english
	}
	if s, ok := t.words[english]; ok {
		return s
	}
	// A translation that has fallen behind degrades to English, one string at a
	// time, rather than showing nothing.
	return english
}
