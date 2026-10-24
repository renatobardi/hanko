package secretref

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFindAndIsRef(t *testing.T) {
	got := Find("a ${secret:API_KEY} b ${secret:user/pessoal} c ${secret:API_KEY} ${secret:1x} ${secret:}")
	want := []Ref{{Name: "API_KEY"}, {Name: "pessoal", User: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Find = %v, want %v", got, want)
	}
	if !IsRef("${secret:x.y-z}") || IsRef("x ${secret:a}") || IsRef("${secret:}") {
		t.Fatal("IsRef incorreto")
	}
	if !ValidName("Tok_en-1.a") || ValidName("1abc") || ValidName("") || ValidName("a b") {
		t.Fatal("ValidName incorreto")
	}
}

func TestFindFields(t *testing.T) {
	// #campo (ADR-0068 §1.4): campo do item; sem #campo vale o campo padrão "value".
	got := Find("${secret:gh#password} ${secret:gh} ${secret:user/p#totp-seed} ${secret:gh#value} ${secret:gh#1x} ${secret:gh#}")
	want := []Ref{
		{Name: "gh", Field: "password"},
		{Name: "gh"},
		{Name: "p", Field: "totp-seed", User: true},
		{Name: "gh", Field: "value"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Find = %v, want %v", got, want)
	}
	if got[1].FieldName() != DefaultField {
		t.Fatalf("FieldName sem #campo = %q, want %q", got[1].FieldName(), DefaultField)
	}
	if !IsRef("${secret:gh#password}") || IsRef("x ${secret:gh#password}") {
		t.Fatal("IsRef incorreto com campo")
	}
	if !ValidFieldName("Campo_1-x") || ValidFieldName("1x") || ValidFieldName("") || ValidFieldName("a b") {
		t.Fatal("ValidFieldName incorreto")
	}
	if s := (Ref{Name: "gh", Field: "password"}).String(); s != "${secret:gh#password}" {
		t.Fatalf("String com campo = %q", s)
	}
	if s := (Ref{Name: "gh", Field: "value"}).String(); s != "${secret:gh}" {
		t.Fatalf("String com campo padrão = %q (não imprime #value)", s)
	}
}

func TestExpand(t *testing.T) {
	look := func(r Ref) (string, error) {
		if r.Name == "missing" {
			return "", errors.New("não existe")
		}
		return "V-" + r.Name, nil
	}
	out, used, err := Expand("Bearer ${secret:tok} / ${secret:tok} / ${secret:b}", look)
	if err != nil || out != "Bearer V-tok / V-tok / V-b" {
		t.Fatalf("Expand = %q, %v", out, err)
	}
	if len(used) != 2 {
		t.Fatalf("used = %v (cada segredo conta uma vez)", used)
	}
	if out, _, err := Expand("sem ref", look); err != nil || out != "sem ref" {
		t.Fatal("texto sem referência deve passar intacto")
	}
	_, _, err = Expand("x ${secret:missing}", look)
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("err = %v, want ErrUnresolved", err)
	}
}

func TestExpandFields(t *testing.T) {
	// Campos diferentes do mesmo item são buscas distintas; o erro nomeia a referência com o campo.
	seen := map[string]bool{}
	look := func(r Ref) (string, error) {
		k := r.Name + "#" + r.FieldName()
		if seen[k] {
			return "", errors.New("buscou de novo: " + k)
		}
		seen[k] = true
		return "V-" + k, nil
	}
	out, used, err := Expand("${secret:gh#user} ${secret:gh#pass} ${secret:gh}", look)
	if err != nil || out != "V-gh#user V-gh#pass V-gh#value" {
		t.Fatalf("Expand = %q, %v", out, err)
	}
	if len(used) != 3 {
		t.Fatalf("used = %v", used)
	}
	_, _, err = Expand("${secret:gh#missing}", func(r Ref) (string, error) { return "", errors.New("não existe") })
	if !errors.Is(err, ErrUnresolved) || !strings.Contains(err.Error(), "${secret:gh#missing}") {
		t.Fatalf("err = %v, want ErrUnresolved com a referência", err)
	}
}

func TestExpandJSON(t *testing.T) {
	in := map[string]any{"env": map[string]any{"A": "${secret:a}", "B": "lit"}, "args": []any{"--k", "${secret:a}"}, "n": 3.0}
	out, used, err := ExpandJSON(in, func(r Ref) (string, error) { return "X", nil })
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["env"].(map[string]any)["A"] != "X" || m["args"].([]any)[1] != "X" || m["n"] != 3.0 {
		t.Fatalf("expansão errada: %v", out)
	}
	if len(used) != 2 {
		t.Fatalf("used = %v", used)
	}
	if in["env"].(map[string]any)["A"] != "${secret:a}" {
		t.Fatal("a entrada não pode ser alterada")
	}
}
