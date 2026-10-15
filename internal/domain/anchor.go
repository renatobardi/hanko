package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// AnchorTarget says which document an anchored comment points into.
type AnchorTarget string

const (
	// AnchorTaskDescription is the description of the task the comment is on.
	AnchorTaskDescription AnchorTarget = "task_description"
	// AnchorRunSummary is the summary (the delivery) of a run of the task.
	AnchorRunSummary AnchorTarget = "run_summary"
)

// Valid reports whether t is a known target.
func (t AnchorTarget) Valid() bool { return t == AnchorTaskDescription || t == AnchorRunSummary }

// MaxAnchorQuoteRunes caps the quoted text of an anchor.
const MaxAnchorQuoteRunes = 2000

// Anchor ties a comment to a passage of a document: the quoted text, where it was (offsets in Unicode
// code points of the document, [Start, End)) and a hash of the whole document when the comment was made.
// The quote is the source of truth: when the document changes the passage is looked up again by its text.
type Anchor struct {
	Target  AnchorTarget `json:"target"`
	RunID   *ID          `json:"run_id,omitempty"` // required when Target is run_summary
	Quote   string       `json:"quote"`
	Start   int          `json:"start"`
	End     int          `json:"end"`
	DocHash string       `json:"doc_hash"`
}

// Anchor resolution states.
const (
	AnchorExact = "exact" // the document is the one the comment was made on
	AnchorMoved = "moved" // the document changed but the quote was found again
	AnchorLost  = "lost"  // the quote is gone from the document
)

// AnchorState is where an anchor points in the current version of its document.
type AnchorState struct {
	Status string `json:"status" enum:"exact,moved,lost"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
}

// NormalizeDoc is the form of a document that anchors refer to: line endings as \n (the browser does the same
// to the text it shows, so offsets and quotes agree on both sides).
func NormalizeDoc(doc string) string {
	if !strings.Contains(doc, "\r") {
		return doc
	}
	return strings.ReplaceAll(strings.ReplaceAll(doc, "\r\n", "\n"), "\r", "\n")
}

// DocHash is a short stable fingerprint of a document (first 16 hex digits of the SHA-256 of its normalized text).
func DocHash(doc string) string {
	sum := sha256.Sum256([]byte(NormalizeDoc(doc)))
	return hex.EncodeToString(sum[:8])
}

// Locate finds the anchor's quote in doc. With the same document the stored offsets hold (exact);
// otherwise the occurrence of the quote closest to the old offset wins (moved); none means lost.
func (a Anchor) Locate(doc string) AnchorState {
	doc = NormalizeDoc(doc)
	if a.Quote == "" {
		return AnchorState{Status: AnchorLost}
	}
	qn := utf8.RuneCountInString(a.Quote)
	if a.DocHash != "" && a.DocHash == DocHash(doc) {
		if s, ok := runeSlice(doc, a.Start, a.End); ok && s == a.Quote {
			return AnchorState{Status: AnchorExact, Start: a.Start, End: a.End}
		}
	}
	best, bestDist, found := 0, 0, false
	byteFrom, runeAt := 0, 0 // runeAt = rune offset of doc[byteFrom:]
	for byteFrom <= len(doc) {
		i := strings.Index(doc[byteFrom:], a.Quote)
		if i < 0 {
			break
		}
		start := runeAt + utf8.RuneCountInString(doc[byteFrom:byteFrom+i])
		d := start - a.Start
		if d < 0 {
			d = -d
		}
		if !found || d < bestDist {
			best, bestDist, found = start, d, true
		}
		// continue after the first rune of this match so overlapping occurrences are seen too
		_, w := utf8.DecodeRuneInString(doc[byteFrom+i:])
		runeAt = start + 1
		byteFrom += i + w
	}
	if !found {
		return AnchorState{Status: AnchorLost}
	}
	status := AnchorMoved
	if a.DocHash != "" && a.DocHash == DocHash(doc) {
		status = AnchorExact
	}
	return AnchorState{Status: status, Start: best, End: best + qn}
}

// runeSlice returns doc[start:end] counted in code points.
func runeSlice(doc string, start, end int) (string, bool) {
	if start < 0 || end < start {
		return "", false
	}
	r := []rune(doc)
	if end > len(r) {
		return "", false
	}
	return string(r[start:end]), true
}
