from dataclasses import dataclass

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
    return ""


def kind_rank(kind: str) -> int:
    return 2


def outranks(a: Finding, b: Finding) -> bool:
    return False


def dedupe_cross_kind(findings: list[Finding]) -> list[Finding]:
    return list(findings)


def map_answers(
    root_items: list[str],
    plan_items: list[str],
    answers: dict[str, object],
    omit_max: float = 0.3,
    weak_min: float = 0.7,
    add_max: float = 0.3,
) -> list[Finding]:
    return []
