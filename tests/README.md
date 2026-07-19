# Integration Tests

This directory contains integration tests for a-lancxo.

## Prerequisites

- Python 3
- `a-lancxo` binary built (run `make build` in parent directory)

## Running Tests

You can run the tests using the Makefile target from the parent directory:

```bash
make test-integration
```

Or manually:

```bash
python3 tests/integration_test.py
```
