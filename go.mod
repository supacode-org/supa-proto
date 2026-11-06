module supaproto

go 1.24.1

require (
	golang.org/x/net v0.50.0
	golang.org/x/sys v0.41.0
	gvisor.dev/gvisor v0.0.0-20260108002349-ace9117eb232
)

require (
	github.com/google/btree v1.1.2 // indirect
	golang.org/x/mobile v0.0.0-20260209203831-923679eb55af // indirect
	golang.org/x/mod v0.33.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/time v0.12.0 // indirect
	golang.org/x/tools v0.42.0 // indirect
)

tool (
	golang.org/x/mobile/cmd/gobind
	golang.org/x/mobile/cmd/gomobile
)
