package service

// AllowFileSkillSources aceita fontes file:// (repositórios locais criados nos testes). Só existe nos testes:
// em produção uma fonte é sempre https (ADR-0035).
func AllowFileSkillSources(s *Service) { s.SkillSources.allowFile = true }
