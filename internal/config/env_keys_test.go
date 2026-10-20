package config

import (
	"reflect"
	"testing"
)

// Toda tag env:"HANKO_*" de Config precisa estar na lista de environFor; senão a variável é ignorada em silêncio
// (aconteceu com HANKO_PLUGINS_DIR).
func TestEveryEnvTagIsLoaded(t *testing.T) {
	loaded := environFor(func(string) string { return "x" })
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if k := f.Tag.Get("env"); k != "" && k != "-" {
				if _, ok := loaded[k]; !ok {
					t.Errorf("%s (campo %s) não está em environFor", k, f.Name)
				}
			}
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type)
			}
		}
	}
	walk(reflect.TypeOf(Config{}))
}

func TestPluginsDirFromEnv(t *testing.T) {
	env := map[string]string{"HANKO_PLUGINS_DIR": "/home/node/plugins", "HANKO_DATABASE_URL": "postgres://u:p@h:5432/d", "HANKO_MODE": "trusted"}
	c, err := LoadFrom(func(k string) string { return env[k] }, func(string) ([]byte, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if c.Plugins.Dir != "/home/node/plugins" {
		t.Fatalf("Plugins.Dir = %q", c.Plugins.Dir)
	}
}
