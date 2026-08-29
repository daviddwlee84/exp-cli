+++
schema = "exp.plan/v1"
id = "plan_01a01e66-f8e0-7202-8000-000000000202"
title = "Calibrate encoder learning rate"
created_at = 2026-08-20T09:01:00Z
updated_at = 2026-08-20T10:03:00Z
priority = "P1"
effort = "S"
state = "completed"
resulting_experiment = "exp_01a01e67-e340-7303-8000-000000000303"
tags = ["encoder", "optimization"]

[expected_payoff]
summary = "Avoid regressions while reducing repeated calibration runs"
metric = "macro_f1"
unit = "score"
estimate = 0.03
+++

# Calibrate encoder learning rate

Test one learning-rate change under a frozen split and training protocol before changing the project default.
