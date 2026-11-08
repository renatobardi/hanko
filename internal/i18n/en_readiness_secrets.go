package i18n

// enReadinessSecrets holds the English texts of the readiness step "secrets" (the adapter_config references resolve and the
// persona reaches them) and of the family-name model refusal.
var enReadinessSecrets = map[string]string{
	"Segredos referenciados": "Referenced secrets",
	"segredos":               "secrets",
}

func init() {
	for k, v := range enReadinessSecrets {
		if _, dup := en[k]; !dup {
			en[k] = v
		}
	}
}
