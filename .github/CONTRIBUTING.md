# Contributing

Thanks for your interest in contributing! By participating, you agree to abide by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Quick start

1. Fork the repo and create a branch from `main`.
2. Make your changes with clear, focused commits.
3. Run the same checks as CI before opening a PR:

```bash
gofmt -w cmd internal
make lint    # gofmt check + go vet
make test
```

   Tests need OpenCV 4.12 (the version gocv v0.42 targets). If your system has
   a different version, run them in the image CI uses:

```bash
docker run --rm -v "$PWD":/src -w /src ghcr.io/hybridgroup/opencv:4.12.0 go test ./...
```

4. Open a pull request and describe the change and motivation.

## Guidelines

- Keep PRs focused on a single change.
- Update docs and `config.yaml.example` when behavior, flags or config keys change.
- Prefer small, readable functions and clear names.
- Avoid committing generated files unless required.

## Reporting issues

Please include:

- OS and version
- Go version (`go version`; the project needs Go 1.25+, see `go.mod`)
- OpenCV version (`pkg-config --modversion opencv4` or `--modversion opencv`)
- Steps to reproduce
- Logs with `-log-level debug`

For security vulnerabilities, see [SECURITY.md](SECURITY.md).
