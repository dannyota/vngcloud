# Documentation

Each fact lives in one place. Link to it instead of copying it.

| Path | Holds | Lifecycle |
|-|-|-|
| [`wiki/`](wiki/Home.md) | User docs for the SDK and CLI, published to the [GitHub wiki](https://github.com/dannyota/vngcloud/wiki) | Updated with each change to the public surface |
| [`design/`](design/README.md) | Intended SDK and CLI design | Approved before code starts |
| [`adr/`](adr/README.md) | One decision per record, with its reasons | Accepted records are never rewritten; a new record supersedes |
| `plans/` | Work order for an active multi-step goal | Deleted when the work ends |

Current behavior is defined by the code and tests. When a doc disagrees with
the code, fix the doc in the same change.
