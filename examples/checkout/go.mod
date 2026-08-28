module github.com/wow-qe/phase-go/examples/checkout

go 1.25.0

require (
	github.com/wow-qe/phase-go v0.1.0
	github.com/wow-qe/phase-go/x/comparators v0.1.0
	github.com/wow-qe/phase-go/x/config v0.1.0
)

require (
	github.com/google/go-cmp v0.7.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/wow-qe/phase-go => ../..
	github.com/wow-qe/phase-go/x/comparators => ../../x/comparators
	github.com/wow-qe/phase-go/x/config => ../../x/config
)
