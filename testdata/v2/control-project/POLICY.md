+++
schema = "exp.policy/v1"
created_at = 2026-08-30T08:00:00Z
updated_at = 2026-08-30T08:00:00Z
autonomy = "manual"
exploit_share = 0.8
explore_share = 0.2
score_formula = "utility-v1"
tie_policy = "keep_incumbent"
promotion_requires_human = true

[taxonomy]
domains = ["ml"]
work = ["training"]
methods = ["ablation"]
components = ["encoder"]

[cluster_saturation]
budget_hours = 24.0
plateau_window = 5
minimum_improvement = 0.01
minimum_probability = 0.1

[[clusters]]
name = "encoder"
state = "open"
budget_hours = 24.0
plateau_window = 5
minimum_improvement = 0.01
minimum_probability = 0.1
+++

# Queue policy

