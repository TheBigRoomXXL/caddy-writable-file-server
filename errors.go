package caddy_writable_file_server

type ErrorDeployement struct {
	StatusCode int
	Private    error
	Public     string
}

func (e ErrorDeployement) Error() string {
	return e.Private.Error()
}
