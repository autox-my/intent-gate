DEFAULT_BASE = "https://api.typesafe.ai"
DEFAULT_MODEL = "jev-latest"
MAX_RESP_BYTES = 1 << 20


def validate_base_url(raw: str) -> str:
    raise ValueError("not implemented")


def ask_jev(base_url: str, key: str, req_body: dict, timeout_s: float = 10.0) -> tuple[str, dict[str, float]]:
    raise RuntimeError("not implemented")


def run_dataset(
    dataset_rows: list[dict],
    base_url: str,
    key: str,
    model: str = DEFAULT_MODEL,
    timeout_s: float = 10.0,
) -> list[dict]:
    return []
