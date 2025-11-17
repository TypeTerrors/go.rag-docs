# go-rag-pack

Generate RAG-ready documentation bundles for your Go projects without digging through the entire module cache. `go-rag-pack` discovers the packages you actually use, lets you curate them in a Charmbracelet/huh form, and emits JSONL chunks that AnythingLLM (or any embedder) can ingest.

## Why use it?

- Focused context. Only packages imported by your project and the modules you explicitly select are processed.
- Choice-driven. Decide whether to include project code, stdlib docs, and third-party libraries through a guided TUI.
- RAG-friendly output. Produces newline-delimited JSON with symbol metadata, perfect for AnythingLLM uploads.

## Install

```bash
go install github.com/natedelduca/go-rag-pack/cmd/go-rag-pack@latest
```

Or run straight from source inside the repo:

```bash
go run ./cmd/go-rag-pack --help
```

## Quick start

From your project root:

```bash
# 1. Create (or refresh) the config file with sensible defaults.
go-rag-pack init

# 2. Launch the Charmbracelet/huh selector to choose what to include.
go-rag-pack select

# 3. Build the JSONL doc pack.
go-rag-pack build
```

You will find the bundle at `./rag/go_docs.jsonl` (configurable).

## One-shot build

Skip the TUI and grab everything the tool discovers automatically:

```bash
go-rag-pack build --auto
```

This includes project code, stdlib packages that appear in the dependency graph, and every third-party module that `go list` detects.

## Upload to AnythingLLM

1. Create an AnythingLLM workspace for your Go project.
2. Upload `rag/go_docs.jsonl`.
3. The workspace now contains:
   - Your project’s source (chunked per symbol).
   - Docs for the third-party modules you selected.
   - Optional stdlib packages you rely on.

Ask AnythingLLM for new handlers or services and it will ground responses in the actual code you work with.

## Configuration notes

- The CLI stores preferences in `.go-rag-pack.json` by default.
- `--config` lets you point to a different config file.
- `--output` overrides the JSONL location during `build`.
- Add extra modules that were not auto-detected in the “Extra modules” input when running `select`.

