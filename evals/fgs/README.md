# FGS Fixed Evaluation Set

`cases.jsonl` is the stable, offline regression contract for the Pi/FGS/ARL control loop. It intentionally uses mocked tool responses only: no live targets, no network access, and no model-quality dependency.

The executable coverage is in `internal/handler/fgs_arl_controller_test.go` and `internal/database/experience_test.go`.

Run it with:

```powershell
go test ./internal/handler ./internal/database ./internal/fgs
```

When adding a new execution rule, add its case here first, then add a deterministic test. A case is only removed when its behavior is deliberately retired.
