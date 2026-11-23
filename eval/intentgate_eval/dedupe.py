from dataclasses import dataclass
import math

OMIT_TEXT = "The plan does not appear to cover this part of what you asked for."
WEAK_TEXT = "The plan appears to loosen this obligation."
ADD_TEXT = "The plan adds this, and it does not appear to be asked for or implied by the root intent."

VALID_KINDS = ("omission", "weakening", "addition")


@dataclass(frozen=True)
class Finding:
    kind: str
    quote: str
    text: str
    confidence: float


def normalize_quote(q: str) -> str:
    """Port of Go normalizeQuote: collapse whitespace and lowercase."""
    return " ".join(q.split()).lower()


def kind_rank(kind: str) -> int:
    """Port of Go kindRank: omission (0) > weakening (1) > addition (2)."""
    if kind == "omission":
        return 0
    if kind == "weakening":
        return 1
    return 2


def outranks(a: Finding, b: Finding) -> bool:
    """Port of Go outranks: finite beats NaN, higher confidence wins, then kind_rank."""
    an, bn = math.isnan(a.confidence), math.isnan(b.confidence)
    if an != bn:
        return bn
    if not an and a.confidence != b.confidence:
        return a.confidence > b.confidence
    return kind_rank(a.kind) < kind_rank(b.kind)


def dedupe_cross_kind(findings: list[Finding]) -> list[Finding]:
    """Port of Go dedupeCrossKind: keep winning kind per non-empty normalized quote."""
    best: dict[str, Finding] = {}
    for f in findings:
        key = normalize_quote(f.quote)
        if key and (key not in best or outranks(f, best[key])):
            best[key] = f
    return [f for f in findings if not (key := normalize_quote(f.quote)) or best[key].kind == f.kind]


def extract_noul(answers: dict[str, object], qid: str) -> float | None:
    """Extract valid noul float in [0, 1] from raw float or TypeSafe {type, noul} answer."""
    raw = answers.get(qid)
    if isinstance(raw, dict):
        if raw.get("type", "noul") not in ("", "noul"):
            return None
        raw = raw.get("noul")
    if isinstance(raw, bool) or not isinstance(raw, (int, float)):
        return None
    val = float(raw)
    if math.isnan(val) or val < 0.0 or val > 1.0:
        return None
    return val


def map_answers(
    root_items: list[str],
    plan_items: list[str],
    answers: dict[str, object],
    omit_max: float = 0.3,
    weak_min: float = 0.7,
    add_max: float = 0.3,
) -> list[Finding]:
    """Port of Go Jev.Review threshold mapping followed by cross-kind dedupe."""
    out: list[Finding] = []
    for i, it in enumerate(root_items):
        v = extract_noul(answers, f"omit_{i}")
        if v is not None and v < omit_max:
            out.append(Finding("omission", it, OMIT_TEXT, 1.0 - v))
    for i, it in enumerate(root_items):
        v = extract_noul(answers, f"weak_{i}")
        if v is not None and v > weak_min:
            out.append(Finding("weakening", it, WEAK_TEXT, v))
    for i, it in enumerate(plan_items):
        v = extract_noul(answers, f"add_{i}")
        if v is not None and v < add_max:
            out.append(Finding("addition", it, ADD_TEXT, 1.0 - v))
    return dedupe_cross_kind(out)
