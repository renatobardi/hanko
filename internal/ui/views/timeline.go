package views

// TimelineVM é a linha do tempo do trabalho: faixas por persona com barras por execução, em unidades do viewBox.
type TimelineVM struct {
	Ranges                 []TabLink
	Width, Height, LabelW  int
	Lanes                  []TimelineLane
	Ticks                  []TimelineTick
	NowX                   float64
	Runs, Failed, Personas int
	Busy                   string
}

type TimelineLane struct {
	Name string
	Y, H int
	Bars []TimelineBar
}

type TimelineBar struct {
	X, W        float64
	Y, H        int
	Class       string // run | done | bad | other
	Href, Label string
	Title       string
}

type TimelineTick struct {
	X     float64
	Label string
}
