package channelplane

// scheduler is intentionally small in Phase 1 because each channel cell owns
// same-channel serialization and reactors start at most one effect per cell.
type scheduler struct{}
