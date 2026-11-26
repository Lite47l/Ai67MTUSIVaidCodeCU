import pytest
import insights


@pytest.fixture(autouse=True)
def isolate_insights(monkeypatch, tmp_path):
    """No unexpected upstream calls; feature tests opt into a model stub."""
    insights._labels_cache.clear()
    monkeypatch.setenv("AGENT_LABEL_CACHE", str(tmp_path / "comment-labels.json"))
    monkeypatch.delenv("AGENT_REVIEWED_LABELS", raising=False)
    monkeypatch.setattr(insights, "configured_model", lambda: None)
