+++
schema = "exp.run/v1"
id = "run_01a01e68-cda0-7404-8000-000000000404"
title = "Baseline and candidate comparison"
created_at = 2026-08-20T09:03:00Z
updated_at = 2026-08-20T09:03:00Z
experiment = "exp_01a01e67-e340-7303-8000-000000000303"
role = "batch"
objective = "Evaluate the baseline and candidate learning rates on one frozen synthetic split."
config_digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
data_digest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
seeds = [42]
expected_outputs = ["artifacts/metrics.json"]
+++

# Baseline and candidate comparison

The evidence unit contains both arms so the comparison shares data, seed, tokenizer, and evaluation code.
