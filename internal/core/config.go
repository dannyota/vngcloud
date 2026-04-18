package core

type Config struct {
	Region    string
	ProjectID string
	IAMUser   *IAMUserAuth
	UserAgent string
}

type EndpointOverrides struct {
	VServer            string
	VLB                string
	VNetwork           string
	GlobalLoadBalancer string
	GLB                string
	DNS                string
	ContainerRegistry  string
	VCR                string
	Portal             string
	Signin             string
	Token              string
}
