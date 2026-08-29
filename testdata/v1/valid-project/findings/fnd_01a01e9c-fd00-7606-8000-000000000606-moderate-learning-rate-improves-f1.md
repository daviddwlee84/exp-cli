+++
schema = "exp.finding/v1"
id = "fnd_01a01e9c-fd00-7606-8000-000000000606"
title = "Moderate learning rate improves macro-F1"
created_at = 2026-08-20T10:00:00Z
updated_at = 2026-08-20T10:00:00Z
statement = "A learning rate of 0.0003 improved synthetic held-out macro-F1 by 0.04 over 0.0001 under the frozen protocol."
scope = "Synthetic encoder fixture using the recorded split, seed, tokenizer, batch size, epochs, and evaluation code."
weakens = []
overturns = []
tags = ["encoder", "optimization"]

[[evidence]]
kind = "run"
ref = "run_01a01e68-cda0-7404-8000-000000000404"
detail = "Included in the concluded Experiment under the locked comparability specification."
+++

# Moderate learning rate improves macro-F1

This finding is deliberately narrow: it does not claim the same effect for another dataset, architecture, or training protocol.
