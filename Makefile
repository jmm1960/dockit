.PHONY: test integration image

test:
	env -u NO_COLOR go test ./...

image:
	docker build -t dockit:local .

integration:
	python3 scripts/integration.py
