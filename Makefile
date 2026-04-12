CGO_CFLAGS := -DSQLITE_ENABLE_FTS5

.PHONY: build install clean test

build:
	CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS)" go build -o lcm ./cmd/lcm/

install: build
	cp lcm $(HOME)/.local/bin/lcm

clean:
	rm -f lcm
	rm -f $(HOME)/.claude/lcm.db

test:
	CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS)" go test ./...
