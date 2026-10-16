package views

type LiveLine struct{ Kind, Text string }

type LiveCard struct {
	Persona, Name, Href         string
	Subject, SubjectHref, Title string
	Agent, Elapsed, Cost        string
	CancelHref                  string
	Lines                       []LiveLine
}

type LiveVM struct {
	Self   string
	Cards  []LiveCard
	Queued int
}
