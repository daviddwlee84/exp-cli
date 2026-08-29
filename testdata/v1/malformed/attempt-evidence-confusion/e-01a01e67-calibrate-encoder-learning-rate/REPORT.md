+++
schema = "exp.experiment/v1"
id = "exp_01a01e67-e340-7303-8000-000000000303"
title = "Calibrate encoder learning rate"
created_at = 2026-08-20T09:02:00Z
updated_at = 2026-08-20T10:02:00Z
lifecycle = "closed"
closure = "concluded"
verdict = "supported"
tags = ["encoder", "optimization"]

[design]
question = "Which encoder learning rate improves held-out macro-F1 under a frozen training protocol?"
hypothesis = "A learning rate of 0.0003 improves macro-F1 by at least 0.03 over the 0.0001 baseline."
kind = "single_factor"
primary_factor = "learning_rate"
secondary_factors = []
baseline = "learning_rate=0.0001"
comparability_spec = "same split, seed, tokenizer, batch size, epochs, and evaluation code"
success_criteria = ["candidate macro-F1 minus baseline macro-F1 >= 0.03"]
decision_rule = "Adopt 0.0003 only if the success criterion is met on the held-out split."
design_locked_at = 2026-08-20T09:03:30Z
design_digest = "sha256:5cfa52900fa35b522bc21a2edeead0c90c71b3ac15c4d4cf406190650fd43e5f"

[conclusion]
concluded_at = 2026-08-20T09:55:00Z
summary = "The candidate improved synthetic held-out macro-F1 by 0.04 under the locked protocol."

[[conclusion.evidence]]
run = "att_01a01e69-b800-7505-8000-000000000505"
disposition = "included"
reason = ""
+++

# Calibrate encoder learning rate

## Result

| Arm | Learning rate | Macro-F1 |
|---|---:|---:|
| Baseline | 0.0001 | 0.71 |
| Candidate | 0.0003 | 0.75 |

The synthetic delta is `0.04`, which satisfies the pre-registered threshold. No operational failure was interpreted as scientific evidence.
