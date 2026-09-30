package app

// power_tracks_exempt.go — the #114 tie-breaker between two tools that used to
// contradict each other.
//
// `pcb power-planes` legitimately decides that a power net must be ROUTED AS
// TRACKS: a 4-layer board has one inner plane for GND and one for the largest
// power net, so a third power net (e.g. VDD_SPI next to 3V3) cannot pour without
// carving up the plane its neighbour owns. It records that verdict in the
// project's workflow state (State.PowerTracksNets).
//
// The post_route_checked gate (布完必查) blocks on `power-not-poured`, which is
// exactly what such a net produces — so before this split the agent deadlocked:
// pouring collides with the plane owner, not pouring fails the gate. The gate now
// consumes the recorded verdict: those findings are still surfaced, just not
// blocking.

// splitPowerNotPoured partitions the `power-not-poured` findings into the ones
// that BLOCK the post-route gate and the ones a prior `pcb power-planes` run
// already excused (its net was routed as tracks on purpose — #114 — or poured
// into an inner PLANE that pour.list can no longer see — #117). Findings of any
// other type are ignored — callers keep counting those themselves.
//
// An INFO-level finding never blocks either: the check itself downgrades a
// power-not-poured to INFO when the board carries a PLANE layer whose pour is
// platform-invisible (#110/#117) — gating on it would deadlock a project with
// no workflow state (standalone `pcb check` boards, stale state).
//
// isExempt is the membership test (state-recorded verdicts); a nil test exempts
// nothing, so a project with no recorded verdict gates exactly as before.
func splitPowerNotPoured(findings []pcbCheckFinding, isExempt func(net string) bool) (blocking, exempt []pcbCheckFinding) {
	for _, fd := range findings {
		if fd.Type != "power-not-poured" {
			continue
		}
		if fd.Level == "INFO" || (isExempt != nil && isExempt(fd.Net)) {
			exempt = append(exempt, fd)
			continue
		}
		blocking = append(blocking, fd)
	}
	return blocking, exempt
}
