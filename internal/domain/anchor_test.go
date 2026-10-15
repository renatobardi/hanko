package domain

import "testing"

func TestAnchorLocate(t *testing.T) {
	doc := "Olá, mundo! Olá de novo. Acentuação: ação, coração."
	quote := "Olá de novo"
	start := len([]rune("Olá, mundo! "))
	a := Anchor{Target: AnchorTaskDescription, Quote: quote, Start: start, End: start + len([]rune(quote)), DocHash: DocHash(doc)}

	if st := a.Locate(doc); st.Status != AnchorExact || st.Start != start || st.End != a.End {
		t.Fatalf("exact: %+v", st)
	}
	// text inserted before: the quote is found again, offsets shift (in code points, not bytes)
	moved := "ÁÉ " + doc
	if st := a.Locate(moved); st.Status != AnchorMoved || st.Start != start+3 || st.End != a.End+3 {
		t.Fatalf("moved: %+v", st)
	}
	// repeated quote: the occurrence closest to the old offset wins
	rep := Anchor{Quote: "Olá", Start: 13, End: 16, DocHash: "old"}
	if st := rep.Locate(doc); st.Status != AnchorMoved || st.Start != 12 {
		t.Fatalf("closest: %+v", st)
	}
	// overlapping occurrences are seen
	ov := Anchor{Quote: "aa", Start: 1, End: 3, DocHash: "old"}
	if st := ov.Locate("aaa"); st.Status != AnchorMoved || st.Start != 1 {
		t.Fatalf("overlap: %+v", st)
	}
	// gone
	if st := a.Locate("nada a ver"); st.Status != AnchorLost {
		t.Fatalf("lost: %+v", st)
	}
	if st := (Anchor{}).Locate(doc); st.Status != AnchorLost {
		t.Fatalf("empty quote: %+v", st)
	}
	// same hash but stale offsets still resolves by text
	bad := Anchor{Quote: quote, Start: 0, End: 3, DocHash: DocHash(doc)}
	if st := bad.Locate(doc); st.Status != AnchorExact || st.Start != start {
		t.Fatalf("bad offsets: %+v", st)
	}
}

func TestAnchorCRLF(t *testing.T) {
	crlf := "linha um\r\nlinha dois\r\nfim"
	lf := "linha um\nlinha dois\nfim"
	if DocHash(crlf) != DocHash(lf) {
		t.Fatal("fins de linha diferentes não podem mudar o hash")
	}
	a := Anchor{Quote: "um\nlinha dois", Start: 6, End: 19, DocHash: DocHash(lf)}
	if st := a.Locate(crlf); st.Status != AnchorExact || st.Start != 6 {
		t.Fatalf("crlf: %+v", st)
	}
}
