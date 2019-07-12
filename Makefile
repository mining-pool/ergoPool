.PHONY: build

build:
	CGO_CFLAGS_ALLOW='-maes.*' go build -o ./bin/ngPool
	