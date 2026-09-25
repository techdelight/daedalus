# Postmortem Creation Instructions

Adapted from Google’s [Postmortem Culture: Learning from Failure](https://sre.google/workbook/postmortem-culture/).

Create a **blameless postmortem** from the supplied incident information. Explain what happened, why it happened, how recovery worked, and what will reduce recurrence or impact. Focus on systems and processes; avoid personal judgments.

Write for readers outside the responding team. Define unfamiliar terminology. Support conclusions with evidence, distinguish estimates from measurements, and mark missing information as unknown.

Include:

1. **Incident details:** Title, date, affected services, document status, one accountable owner, and contributors.
2. **Executive summary:** Incident, user impact, causes, and resolution.
3. **Impact:** Quantify scope, duration, failed requests, latency, and business consequences where available.
4. **Background:** Explain relevant architecture and dependencies.
5. **Timeline and recovery:** Record timestamped events, detection, decisions, mitigation, and restoration. Specify the timezone.
6. **Causes and trigger:** Separate the initiating event from underlying defects and contributing conditions. Explain why safeguards failed.
7. **Lessons learned:** Describe what worked, what failed, and where luck limited damage.
8. **Action items:** Provide concrete, measurable improvements with type, priority, individual owner, and tracking reference. Favor systemic prevention over instructions to be more careful.
9. **References:** Link supporting evidence and define terminology.

Have participants review the draft, publish promptly, share broadly with appropriate access, and track actions to completion.

## Incident Information

[Insert incident notes, logs, metrics, and references here.]

