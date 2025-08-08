package caddy_site_deployer

type ErrorDeployement struct {
	StatusCode int
	Private    error
	Public     string
}

func (e ErrorDeployement) Error() string {
	return e.Private.Error()
}
