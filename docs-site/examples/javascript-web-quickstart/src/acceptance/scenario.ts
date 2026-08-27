const ACCEPTANCE_RUN_ID = /^[a-z0-9][a-z0-9-]{0,31}$/;

export interface AcceptanceParticipantUids {
  aliceUid: string;
  bobUid: string;
}

/** Builds isolated development identities without exceeding the Product HTTP UID contract. */
export function acceptanceParticipantUids(
  runId: string,
): AcceptanceParticipantUids {
  if (!ACCEPTANCE_RUN_ID.test(runId)) {
    throw new Error(
      "acceptance run ID must use 1-32 lowercase letters, numbers, or hyphens",
    );
  }
  return {
    aliceUid: `docs-alice-${runId}`,
    bobUid: `docs-bob-${runId}`,
  };
}
