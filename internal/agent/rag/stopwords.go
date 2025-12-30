package rag

import "unicode"

// stopwords contains common stopwords for Japanese and English
var stopwords = map[string]bool{
	// Japanese particles and auxiliary words
	"は": true, "が": true, "を": true, "に": true, "へ": true, "と": true, "から": true, "より": true,
	"で": true, "の": true, "も": true, "や": true, "か": true, "など": true, "ばかり": true,
	"だけ": true, "まで": true, "ほど": true, "くらい": true, "ぐらい": true,
	"です": true, "である": true, "だ": true, "であります": true,
	"ます": true, "ました": true, "ません": true, "ましょう": true,
	"た": true, "て": true,
	"れる": true, "られる": true, "せる": true, "させる": true,
	"する": true, "した": true, "して": true, "される": true,
	"ある": true, "いる": true, "おる": true, "なる": true, "できる": true,
	"こと": true, "もの": true, "ところ": true, "ため": true, "とき": true,
	"これ": true, "それ": true, "あれ": true, "どれ": true,
	"この": true, "その": true, "あの": true, "どの": true,
	"ここ": true, "そこ": true, "あそこ": true, "どこ": true,
	"私": true, "あなた": true, "彼": true, "彼女": true,
	"一": true, "二": true, "三": true, "四": true, "五": true,
	"六": true, "七": true, "八": true, "九": true, "十": true,
	"年": true, "月": true, "日": true, "時": true, "分": true, "秒": true,

	// English common stopwords
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "been": true, "by": true, "for": true, "from": true,
	"has": true, "he": true, "in": true, "is": true, "it": true, "its": true,
	"of": true, "on": true, "that": true, "the": true, "to": true,
	"was": true, "were": true, "will": true, "with": true,
	"i": true, "you": true, "we": true, "they": true, "this": true, "these": true,
	"those": true, "what": true, "which": true, "who": true, "when": true,
	"where": true, "why": true, "how": true,
	"can": true, "could": true, "should": true, "would": true, "may": true, "might": true,
	"have": true, "had": true, "do": true, "does": true, "did": true,
	"not": true, "no": true, "yes": true,
	"but": true, "or": true, "so": true, "if": true, "then": true, "than": true,
	"up": true, "down": true, "out": true, "off": true, "over": true, "under": true,
	"more": true, "most": true, "less": true, "least": true,
	"very": true, "much": true, "many": true, "some": true, "any": true, "all": true,

	// Common noisy tokens (punctuation / separators / markdown / URLs)
	"#": true, "##": true, "###": true, "####": true, "#####": true, "######": true,
	":": true, "::": true, ";": true, ",": true, ".": true, "..": true, "...": true,
	"/": true, "\\": true, "//": true, "://": true,
	"(": true, ")": true, "[": true, "]": true, "{": true, "}": true,
	"<": true, ">": true, "\"": true, "'": true,
	"`": true, "``": true, "```": true,
	"-": true, "--": true, "---": true, "—": true, "–": true,
	"*": true, "**": true, "***": true,
	"_": true, "__": true,
	"|": true, "||": true,
	"=": true, "==": true, "===": true,
}

// isNoiseToken returns true if the token is composed only of punctuation/symbols/spaces.
// This aggressively removes separators like "#", ":", "://", ".", "/" which commonly appear after tokenization.
func isNoiseToken(token string) bool {
	hasNonSpace := false
	for _, r := range token {
		if unicode.IsSpace(r) {
			continue
		}
		hasNonSpace = true

		// Keep any token that contains letters/numbers or Japanese scripts.
		if unicode.IsLetter(r) || unicode.IsNumber(r) ||
			(r >= 0x3040 && r <= 0x309F) || // Hiragana
			(r >= 0x30A0 && r <= 0x30FF) || // Katakana
			(r >= 0x4E00 && r <= 0x9FAF) { // CJK ideographs
			return false
		}
	}
	return hasNonSpace
}

// isStopword checks if a token is a stopword
func isStopword(token string) bool {
	if stopwords[token] {
		return true
	}
	return isNoiseToken(token)
}

// filterStopwords removes stopwords from a token list
func filterStopwords(tokens []string) []string {
	var filtered []string
	for _, token := range tokens {
		if !isStopword(token) {
			filtered = append(filtered, token)
		}
	}
	return filtered
}
