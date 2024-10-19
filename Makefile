all:
	go build
	
arm:
	go env -w GOARCH="arm" GOARM=7
	go build
	go env -w GOARCH="amd64"