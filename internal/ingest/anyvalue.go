package ingest

// anyValue é o `any_value` de otlp.py: o AnyValue do OTLP JSON como valor simples (int64 chega como texto).
func anyValue(v any) (any, error) {
	m, ok := asMap(v)
	if !ok || len(m) == 0 {
		return nil, nil
	}
	if x, ok := m["stringValue"]; ok {
		return x, nil
	}
	if x, ok := m["boolValue"]; ok {
		return truthy(x), nil
	}
	if x, ok := m["intValue"]; ok {
		n, ok := toInt(x)
		if !ok {
			return nil, bad("intValue inválido: %s", Canon(x))
		}
		return n, nil
	}
	if x, ok := m["doubleValue"]; ok {
		f, ok := toFloat(x)
		if !ok {
			return nil, bad("doubleValue inválido: %s", Canon(x))
		}
		return f, nil
	}
	if x, ok := m["arrayValue"]; ok {
		return arrayOf(x)
	}
	if x, ok := m["kvlistValue"]; ok {
		kv, _ := asMap(or(x, map[string]any{}))
		return attrs(getKey(kv, "values"))
	}
	if x, ok := m["bytesValue"]; ok {
		return x, nil
	}
	return nil, nil
}

func arrayOf(x any) (any, error) {
	arr, _ := asMap(or(x, map[string]any{}))
	vals, _ := or(getKey(arr, "values"), []any{}).([]any)
	out := make([]any, 0, len(vals))
	for _, e := range vals {
		v, err := anyValue(e)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// attrs é o `attrs` de otlp.py: a lista de {key, value} como mapa.
func attrs(kvs any) (map[string]any, error) {
	out := map[string]any{}
	list, _ := kvs.([]any)
	for _, e := range list {
		kv, ok := asMap(e)
		if !ok {
			continue
		}
		k, has := kv["key"]
		if !has {
			continue
		}
		ks, isStr := k.(string)
		if !isStr {
			ks = Canon(k)
		}
		v, err := anyValue(kv["value"])
		if err != nil {
			return nil, err
		}
		out[ks] = v
	}
	return out, nil
}
