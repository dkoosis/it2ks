module github.com/dkoosis/it2ks

go 1.26.3

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/tmc/it2 v0.0.0
)

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	golang.org/x/sys v0.36.0 // indirect
	golang.org/x/term v0.35.0 // indirect
	google.golang.org/protobuf v1.35.2 // indirect
)

replace github.com/tmc/it2 => ../it2
