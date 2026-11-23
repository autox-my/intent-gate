import json
import time
import urllib.error
import urllib.parse
import urllib.request

from .dedupe import extract_noul
from .items import MAX_ITEMS_EACH, build_jev_request, items

DEFAULT_BASE = "https://api.typesafe.ai"
DEFAULT_MODEL = "jev-latest"
JEV_PATH = "/v1/systemone"
MAX_RESP_BYTES = 1 << 20
_LOOPBACK_HOSTS = {"127.0.0.1", "::1", "localhost"}


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise RuntimeError(f"jev: refusing redirect ({code})")


def _check_scheme(scheme: str, host: str) -> None:
    if scheme == "http" and host not in _LOOPBACK_HOSTS:
        raise ValueError(f"jev: refusing non-https API host {host!r}")
    if scheme not in ("http", "https"):
        raise ValueError("jev: API URL scheme must be https")


def validate_base_url(raw: str) -> str:
    """Validate base URL: https only (http allowed only for loopback), no credentials/query/fragment."""
    trimmed = raw.strip().rstrip("/")
    u = urllib.parse.urlsplit(trimmed)
    host = (u.hostname or "").lower()
    if not host or any((u.username, u.password, "@" in trimmed)):
        raise ValueError("jev: bad API URL")
    if any((u.query, u.fragment, "?" in raw, "#" in raw)):
        raise ValueError("jev: query or fragment is not allowed in API URL")
    _check_scheme(u.scheme, host)
    return trimmed


def _scrub(text: str, key: str) -> str:
    return text.replace(key, "[redacted]") if key else text


def _decode_response(raw: bytes) -> tuple[str, dict[str, float]]:
    try:
        doc = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        raise RuntimeError("jev: bad JSON response") from None
    if not isinstance(doc, dict) or not isinstance(doc.get("answers"), dict):
        raise RuntimeError("jev: bad JSON response (missing answers)")
    answers_raw = doc["answers"]
    cleaned: dict[str, float] = {}
    for qid in sorted(answers_raw):
        v = extract_noul(answers_raw, qid)
        if v is not None:
            cleaned[qid] = v
    return str(doc.get("model", "")), cleaned


def ask_jev(base_url: str, key: str, req_body: dict, timeout_s: float = 10.0) -> tuple[str, dict[str, float]]:
    """POST one systemone request with transport safety and return (model, numeric answers)."""
    if not key:
        raise ValueError("jev: TYPESAFE_API_KEY is empty")
    base = validate_base_url(base_url)
    payload = json.dumps(req_body).encode("utf-8")
    req = urllib.request.Request(
        base + JEV_PATH,
        data=payload,
        method="POST",
        headers={
            "Authorization": f"Bearer {key}",
            "Content-Type": "application/json",
            "Accept": "application/json",
        },
    )
    opener = urllib.request.build_opener(_NoRedirectHandler())
    try:
        with opener.open(req, timeout=timeout_s) as resp:
            if resp.status < 200 or resp.status > 299:
                raise RuntimeError(f"jev: HTTP {resp.status}")
            raw = resp.read(MAX_RESP_BYTES + 1)
    except urllib.error.HTTPError as exc:
        raise RuntimeError(_scrub(f"jev: HTTP {exc.code} {exc.reason}", key)) from None
    except urllib.error.URLError as exc:
        raise RuntimeError(_scrub(f"jev: request failed: {exc.reason}", key)) from None
    except Exception as exc:
        raise RuntimeError(_scrub(str(exc), key)) from None
    if len(raw) > MAX_RESP_BYTES:
        raise RuntimeError(f"jev: response exceeds {MAX_RESP_BYTES} bytes")
    return _decode_response(raw)


def run_dataset(
    dataset_rows: list[dict],
    base_url: str,
    key: str,
    model: str = DEFAULT_MODEL,
    timeout_s: float = 10.0,
) -> list[dict]:
    """Run all dataset cases against Jev and return score records without secrets."""
    results: list[dict] = []
    for case in dataset_rows:
        ritems = items(case["root_intent"], MAX_ITEMS_EACH)
        pitems = items(case["plan"], 0)[:MAX_ITEMS_EACH]
        if not ritems or not pitems:
            results.append({
                "case_id": case["id"],
                "model": model,
                "latency_ms": 0,
                "root_items": ritems,
                "plan_items": pitems,
                "answers": {},
            })
            continue
        req = build_jev_request(case["root_intent"], case["plan"], model=model)
        t0 = time.monotonic()
        resp_model, answers = ask_jev(base_url, key, req, timeout_s=timeout_s)
        latency_ms = int(round((time.monotonic() - t0) * 1000))
        results.append({
            "case_id": case["id"],
            "model": resp_model or model,
            "latency_ms": latency_ms,
            "root_items": ritems,
            "plan_items": pitems,
            "answers": answers,
        })
    return results
