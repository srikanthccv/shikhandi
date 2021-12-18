binary:
	GOOS=linux GOARCH=amd64 go build -o ./bin/shikhandi_linux_amd64
	GOOS=darwin GOARCH=amd64 go build -o ./bin/shikhandi_darwin_amd64
