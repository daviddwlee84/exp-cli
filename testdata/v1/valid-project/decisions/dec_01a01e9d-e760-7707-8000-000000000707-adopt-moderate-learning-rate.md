+++
schema = "exp.decision/v1"
id = "dec_01a01e9d-e760-7707-8000-000000000707"
title = "Adopt the moderate learning rate"
created_at = 2026-08-20T10:01:00Z
updated_at = 2026-08-20T10:01:00Z
statement = "Use learning_rate=0.0003 as the synthetic encoder default for the recorded protocol."
based_on = ["fnd_01a01e9c-fd00-7606-8000-000000000606"]
action = "Update the reviewed training configuration in a separate engineering change."
effective_at = 2026-08-20T10:01:00Z
supersedes = []
tags = ["encoder", "optimization"]
+++

# Adopt the moderate learning rate

The implementation change is intentionally outside the fixture; this record preserves why that action was chosen.
