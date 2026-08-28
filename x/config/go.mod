module github.com/wow-qe/phase-go/x/config

go 1.25.0

require (
	// The real release version a downstream consumer resolves; the
	// replace below serves only in-repo development and is ignored by
	// consumers.
	github.com/wow-qe/phase-go v0.1.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/wow-qe/phase-go => ../..
