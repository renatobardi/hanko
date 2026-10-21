package secretref

import (
	"errors"
	"reflect"
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
