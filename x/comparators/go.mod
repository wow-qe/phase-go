module github.com/wow-qe/phase-go/x/comparators

go 1.25.8

require (
	github.com/google/go-cmp v0.7.0
	// The real release version a downstream consumer resolves; the
	// replace below serves only in-repo development and is ignored by
	// consumers.
	github.com/wow-qe/phase-go v0.1.0
)

replace github.com/wow-qe/phase-go => ../..
