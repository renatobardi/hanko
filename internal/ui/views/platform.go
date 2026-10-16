package views

// EnvCheck é uma verificação de ambiente (ok ou não, com o detalhe que explica).
type EnvCheck struct {
	OK     bool
	Label  string
	Detail string
}

type KV struct{ K, V string }

type EnvironmentsVM struct {
	Sections  []TabLink
	Checks    []EnvCheck
	OK, Total int
	Facts     []KV
}

type AdapterRow struct {
	Key, Name, Kind, Desc string
	Check                 EnvCheck
	Personas              []string
	Extra                 string
}

type PriceRow struct{ Model, In, Out, Since string }

type AdaptersVM struct {
	Sections []TabLink
	Rows     []AdapterRow
	Prices   []PriceRow
}

type PluginItem struct {
	Icon, Name, Desc string
	Checks           []EnvCheck
	Href             string
}

type PluginsVM struct {
	Sections []TabLink
	Items    []PluginItem
}
