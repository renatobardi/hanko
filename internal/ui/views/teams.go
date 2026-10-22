package views

type TeamMemberVM struct {
	Name, Title, Icon, Model string
	Lead                     bool
}

type TeamCard struct {
	Key, Name, Desc, Icon, Action string
	Members                       []TeamMemberVM
}

type TeamCatalogVM struct {
	Notice   string
	Teams    []TeamCard
	Packages PackagesVM // pacotes hanko.team.v1 (ADR-0044)
}
