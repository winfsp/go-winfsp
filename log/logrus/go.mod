module github.com/winfsp/go-winfsp/log/logrus

go 1.25

require (
	github.com/sirupsen/logrus v1.9.4
	github.com/winfsp/go-winfsp v0.0.0
)

require golang.org/x/sys v0.15.0 // indirect

replace github.com/winfsp/go-winfsp => ../..
