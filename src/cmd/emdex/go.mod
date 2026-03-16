module github.com/piotrlaczykowski/emdexer/emdex

go 1.26.1

require (
	github.com/fatih/color v1.18.0
	github.com/piotrlaczykowski/emdexer v1.0.6
	github.com/qdrant/go-client v1.17.1
	google.golang.org/grpc v1.79.2
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260311181403-84a4fc48630c // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/piotrlaczykowski/emdexer => ../../pkg
