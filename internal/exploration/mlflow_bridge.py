"""Bounded exp storage protocol. MLflow owns tracking metadata and artifacts.

Read one private request on stdin; emit only portable identity on stdout.
Errors deliberately omit provider exceptions, credentials and host paths.
"""
import contextlib
import json
import pathlib
import os
import sys


def handle(request):
    from mlflow import MlflowClient

    if request["tracking_uri"].startswith("sqlite:"):
        try:
            import sqlalchemy  # noqa: F401: full local tracking dependencies
            from mlflow.store.tracking.sqlalchemy_store import SqlAlchemyStore  # noqa: F401
        except ImportError:
            return {"error": "mlflow-sqlite-dependencies-missing"}
    client = MlflowClient(tracking_uri=request["tracking_uri"])
    project = request["project"]
    attempt = request["attempt"]
    name = "exp-" + project
    experiment = client.get_experiment_by_name(name)
    if experiment is None:
        if request["action"] == "fetch":
            raise RuntimeError("missing experiment")
        try:
            experiment_id = client.create_experiment(
                name, artifact_location=request.get("artifact_location")
            )
        except Exception:
            experiment = client.get_experiment_by_name(name)
            if experiment is None:
                raise
            experiment_id = experiment.experiment_id
    else:
        experiment_id = experiment.experiment_id

    if request["action"] == "save":
        runs = client.search_runs(
            [experiment_id],
            filter_string="tags.`exp.attempt_id` = '" + attempt + "'",
            max_results=2,
        )
        if len(runs) > 1:
            raise RuntimeError("ambiguous run ownership")
        if runs:
            run = runs[0]
            if run.data.tags.get("exp.project_id") != project:
                raise RuntimeError("run ownership mismatch")
        else:
            run = client.create_run(
                experiment_id,
                tags={"exp.project_id": project, "exp.attempt_id": attempt,
                      "exp.try_id": request["try"], "exp.storage_protocol": "v1"},
            )
        run_id = run.info.run_id
        for artifact in request["artifacts"]:
            local = pathlib.Path(artifact["path"])
            logical = pathlib.PurePosixPath(artifact["name"])
            # The Go caller stages each file with its logical basename.
            parent = str(logical.parent)
            client.log_artifact(run_id, str(local), None if parent == "." else parent)
        client.set_terminated(run_id)
        return {"run_id": run_id}

    run = client.get_run(request["run_id"])
    if (run.data.tags.get("exp.project_id") != project or
            run.data.tags.get("exp.attempt_id") != attempt):
        raise RuntimeError("run ownership mismatch")
    local = client.download_artifacts(
        request["run_id"], request["name"], request["destination"]
    )
    return {"path": local, "run_id": request["run_id"]}


os.umask(0o077)

try:
    request = json.load(sys.stdin)
    # Some SDK versions print tracking notices. Preserve a JSON-only stdout.
    with contextlib.redirect_stdout(sys.stderr):
        response = handle(request)
    print(json.dumps(response))
except ImportError:
    print(json.dumps({"error": "mlflow-not-installed"}))
    sys.exit(2)
except Exception:
    print(json.dumps({"error": "mlflow-operation-failed"}))
    sys.exit(1)
