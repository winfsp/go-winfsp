module github.com/winfsp/go-winfsp/examples/memfs

go 1.25

require (
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.10.2
	github.com/winfsp/go-winfsp v0.0.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.3.0 // indirect
)

replace github.com/winfsp/go-winfsp => ../..
