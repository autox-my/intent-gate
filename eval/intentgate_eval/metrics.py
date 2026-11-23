import itertools

from .dedupe import VALID_KINDS, extract_noul, map_answers, normalize_quote
from .items import MAX_ITEMS_EACH, items

BASELINE = (0.30, 0.70, 0.30)
OMIT_GRID = [round(0.15 + 0.05 * i, 2) for i in range(9)]
WEAK_GRID = [round(0.45 + 0.05 * i, 2) for i in range(9)]
ADD_GRID = [round(0.15 + 0.05 * i, 2) for i in range(9)]


def brier_score(probs: list[float], labels: list[int]) -> float | None:
    if not probs:
        return None
    total = sum((p - y) ** 2 for p, y in zip(probs, labels))
    return round(total / len(probs), 6)


def ece_score(probs: list[float], labels: list[int], bins: int = 10) -> float | None:
    if not probs:
        return None
    buckets: list[list[tuple[float, int]]] = [[] for _ in range(bins)]
    for p, y in zip(probs, labels):
        idx = min(bins - 1, max(0, int(p * bins)))
        buckets[idx].append((p, y))
    n = len(probs)
    ece = 0.0
    for b in buckets:
        if not b:
            continue
        mean_p = sum(p for p, _ in b) / len(b)
        mean_y = sum(y for _, y in b) / len(b)
        ece += (len(b) / n) * abs(mean_y - mean_p)
    return round(ece, 6)


def _index_scores(score_rows: list[dict]) -> dict[str, dict]:
    return {row["case_id"]: row for row in score_rows}


def _case_items(case: dict, score: dict) -> tuple[list[str], list[str]]:
    ritems = score.get("root_items") or items(case["root_intent"], MAX_ITEMS_EACH)
    pitems = score.get("plan_items") or items(case["plan"], 0)[:MAX_ITEMS_EACH]
    return ritems, pitems


def _collect_calibration(dataset_rows: list[dict], by_id: dict[str, dict]) -> dict[str, tuple[list[float], list[int]]]:
    data: dict[str, tuple[list[float], list[int]]] = {k: ([], []) for k in VALID_KINDS}
    for case in dataset_rows:
        score = by_id.get(case["id"])
        if not score:
            continue
        ritems, pitems = _case_items(case, score)
        answers = score.get("answers", {})
        _record_root_cal(data, case, ritems, answers)
        _record_plan_cal(data, case, pitems, answers)
    return data


def _record_root_cal(data: dict, case: dict, ritems: list[str], answers: dict) -> None:
    exp_omit = {normalize_quote(q) for q in case.get("expected_omissions", [])}
    exp_weak = {normalize_quote(q) for q in case.get("expected_weakenings", [])}
    for i, quote in enumerate(ritems):
        nq = normalize_quote(quote)
        ov = extract_noul(answers, f"omit_{i}")
        if ov is not None:
            data["omission"][0].append(1.0 - ov)
            data["omission"][1].append(1 if nq in exp_omit else 0)
        wv = extract_noul(answers, f"weak_{i}")
        if wv is not None:
            data["weakening"][0].append(wv)
            data["weakening"][1].append(1 if nq in exp_weak else 0)


def _record_plan_cal(data: dict, case: dict, pitems: list[str], answers: dict) -> None:
    exp_add = {normalize_quote(q) for q in case.get("expected_additions", [])}
    for j, quote in enumerate(pitems):
        nq = normalize_quote(quote)
        av = extract_noul(answers, f"add_{j}")
        if av is not None:
            data["addition"][0].append(1.0 - av)
            data["addition"][1].append(1 if nq in exp_add else 0)


def _ratio(num: int, den: int) -> float | None:
    return round(num / den, 6) if den > 0 else None


def _summarize_counts(counts: dict[str, int], brier: float | None, ece: float | None) -> dict:
    tp, fp, fn, tn = counts["tp"], counts["fp"], counts["fn"], counts["tn"]
    f1_den = 2 * tp + fp + fn
    return {
        "tp": tp,
        "fp": fp,
        "fn": fn,
        "tn": tn,
        "precision": _ratio(tp, tp + fp),
        "recall": _ratio(tp, tp + fn),
        "f1": _ratio(2 * tp, f1_den) if f1_den > 0 else 0.0,
        "fpr": _ratio(fp, fp + tn),
        "brier": brier,
        "ece": ece,
    }


def _expected_sets(case: dict) -> dict[str, set[str]]:
    return {
        "omission": {normalize_quote(q) for q in case.get("expected_omissions", [])},
        "weakening": {normalize_quote(q) for q in case.get("expected_weakenings", [])},
        "addition": {normalize_quote(q) for q in case.get("expected_additions", [])},
    }


def _update_family_counts(counts: dict[str, dict[str, int]], case: dict, ritems: list[str], pitems: list[str], preds: list) -> None:
    exp_map = _expected_sets(case)
    pred_map = {k: {normalize_quote(f.quote) for f in preds if f.kind == k} for k in VALID_KINDS}
    cands = {
        "omission": set(dict.fromkeys(normalize_quote(q) for q in ritems)),
        "weakening": set(dict.fromkeys(normalize_quote(q) for q in ritems)),
        "addition": set(dict.fromkeys(normalize_quote(q) for q in pitems)),
    }
    for k in VALID_KINDS:
        pred, exp, cand = pred_map[k], exp_map[k], cands[k]
        counts[k]["tp"] += len(pred & exp)
        counts[k]["fp"] += len(pred - exp)
        counts[k]["fn"] += len(exp - pred)
        counts[k]["tn"] += len(cand - (pred | exp))


def _macro_summary(families: dict[str, dict]) -> dict[str, float]:
    pos_fams = [f for f in families.values() if f["tp"] + f["fn"] > 0]
    if not pos_fams:
        return {"precision": 0.0, "f1": 0.0}
    macro_f1 = round(sum(f["f1"] for f in pos_fams) / len(pos_fams), 6)
    macro_prec = round(sum((f["precision"] or 0.0) for f in pos_fams) / len(pos_fams), 6)
    return {"precision": macro_prec, "f1": macro_f1}


def evaluate_scores(
    dataset_rows: list[dict],
    score_rows: list[dict],
    omit_max: float = 0.3,
    weak_min: float = 0.7,
    add_max: float = 0.3,
) -> dict:
    """Evaluate per-family and macro metrics over scored dataset cases post-dedupe."""
    by_id = _index_scores(score_rows)
    cal = _collect_calibration(dataset_rows, by_id)
    counts = {k: {"tp": 0, "fp": 0, "fn": 0, "tn": 0} for k in VALID_KINDS}
    evaluated = 0
    for case in dataset_rows:
        score = by_id.get(case["id"])
        if not score:
            continue
        evaluated += 1
        ritems, pitems = _case_items(case, score)
        preds = map_answers(ritems, pitems, score.get("answers", {}), omit_max, weak_min, add_max)
        _update_family_counts(counts, case, ritems, pitems, preds)

    families = {
        k: _summarize_counts(counts[k], brier_score(*cal[k]), ece_score(*cal[k]))
        for k in VALID_KINDS
    }
    return {
        "cases": evaluated,
        "thresholds": {"omit_max": omit_max, "weak_min": weak_min, "add_max": add_max},
        "families": families,
        "macro": _macro_summary(families),
    }


def _candidate_rank(res: dict, min_precision: float, omit_max: float, weak_min: float, add_max: float) -> tuple[bool, tuple]:
    pos_fams = [f for f in res["families"].values() if f["tp"] + f["fn"] > 0]
    precs = [(f["precision"] if f["precision"] is not None else 0.0) for f in pos_fams]
    met = sum(1 for p in precs if p >= min_precision)
    all_met = bool(pos_fams) and met == len(pos_fams)
    min_p = min(precs) if precs else 0.0
    l1 = abs(omit_max - BASELINE[0]) + abs(weak_min - BASELINE[1]) + abs(add_max - BASELINE[2])
    if all_met:
        return True, (-round(res["macro"]["f1"], 12), -round(res["macro"]["precision"], 12), round(l1, 12), omit_max, weak_min, add_max)
    return False, (-met, -round(min_p, 12), -round(res["macro"]["f1"], 12), -round(res["macro"]["precision"], 12), round(l1, 12), omit_max, weak_min, add_max)


def _pick_best(candidates: list[tuple[tuple, dict]]) -> dict:
    return min(candidates, key=lambda item: item[0])[1]


def sweep_thresholds(
    dataset_rows: list[dict],
    score_rows: list[dict],
    min_precision: float = 0.90,
) -> dict:
    """Grid-search per-kind thresholds maximizing macro-F1 subject to per-family precision >= min_precision."""
    baseline = evaluate_scores(dataset_rows, score_rows, *BASELINE)
    feasible: list[tuple[tuple, dict]] = []
    fallback: list[tuple[tuple, dict]] = []
    for om, wm, am in itertools.product(OMIT_GRID, WEAK_GRID, ADD_GRID):
        res = evaluate_scores(dataset_rows, score_rows, om, wm, am)
        all_met, rank = _candidate_rank(res, min_precision, om, wm, am)
        (feasible if all_met else fallback).append((rank, res))
    meets = bool(feasible)
    winner = _pick_best(feasible if meets else fallback)
    return {
        "grid_size": len(OMIT_GRID) * len(WEAK_GRID) * len(ADD_GRID),
        "min_precision": min_precision,
        "meets_precision_target": meets,
        "baseline": baseline,
        "best": winner,
    }
