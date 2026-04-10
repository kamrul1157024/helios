.PHONY: build frontend clean dev install uninstall test

build: frontend
	touch frontend.go
	go build -o helios ./cmd/helios/
	codesign -s - -f ./helios

install: build
	sudo cp helios /usr/local/bin/helios
	@echo "helios installed to /usr/local/bin/helios"

uninstall:
	sudo rm -f /usr/local/bin/helios
	@echo "helios removed from /usr/local/bin"

frontend:
	rm -rf frontend/dist
	cd frontend && npm install && npm run build

clean:
	rm -f helios
	rm -rf frontend/dist frontend/node_modules

dev:
	cd frontend && npm run dev &
	go run ./cmd/helios/ daemon start

test:
	go test ./...
