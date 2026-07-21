# Backup Contracts Flow

`internal/contracts/backup` contains only lightweight coordination DTOs,
bounded status enums, and cross-layer sentinel errors. It lets node-local
runtime code communicate with the backup usecase through injected ports without
importing `internal/usecase`.

The package does not schedule jobs, mutate Controller state, read storage,
encode repository artifacts, or call infrastructure. Business decisions remain
in `internal/usecase/backup`; runtime receives the pure scheduling function
through composition.
