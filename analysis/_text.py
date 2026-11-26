"""Conservative detection of explicit problem statements in Russian feedback."""

import re
from collections.abc import Iterator

_CLAUSE_BOUNDARY = re.compile(
    r"[.!?;\n]+|\b(?:но|однако|зато|хотя|при этом)\b|"
    r",(?=\s+(?:друг\w*\s+)?(?:проблем|сложност|препятств))",
    re.I,
)
_NO_ISSUE = re.compile(
    r"\b(?:ничего\s+не\s+меша\w*|"
    r"(?:сложност\w*|проблем\w*|препятств\w*|перегруз\w*|неясност\w*)"
    r"[^,]{0,30}\b(?:не\s+был\w*|нет)|"
    r"(?:нет|не\s+был\w*|не\s+испытыва\w*)\s+"
    r"(?:(?:серьезн|существенн|высок)\w*\s+)?"
    r"(?:сложност\w*|проблем\w*|перегруз\w*)"
    r")\b"
)
_RECOVERY = re.compile(
    r"\b(?:вернул\w*\s+к\s+комфортн\w*|"
    r"ст(?:ал\w*|ало)\s+(?:понятнее|проще|регулярнее)|"
    r"нагруз\w*\s+(?:снизил\w*|нормализовал\w*)|"
    r"после\s+снижения\s+нагруз\w*)\b"
)
_NEGATED_HIGH_LOAD = re.compile(r"\b(?:не\s+(?:был\w*\s+)?|нет\s+)высок\w*")

_TOPICS = {
    "task_clarity": (
        r"(?:задач|приоритет|постановк|требован|ожидаем\w*\s+результат)",
        r"\b(?:неясн\w*|непонятн\w*|"
        r"не\s+(?:(?:всегда|сразу)\s+)?понят\w*|"
        r"(?:повторн\w*|поздн\w*|долго|задерж\w*)[^.!?;]{0,55}уточнен\w*|"
        r"(?:часто|повторно)\s+(?:приход\w*\s+)?уточня\w*|"
        r"(?:точнее|яснее)\s+формулиров\w*)\b",
    ),
    "mentor_feedback": (
        r"(?:обратн\w*\s+связ|наставник|регулярн\w*\s+(?:связ|сверк|встреч))",
        r"\b(?:не\s+хватает|нет\s+обратн\w*\s+связ\w*|"
        r"недостаточ\w*|редк\w*|не\s+получа\w*|"
        r"(?:чаще|быстрее)\s+(?:давать|получать|обсуждать)|"
        r"(?:нужн\w*|стоит|хотелось\s+бы)\b.{0,35}"
        r"(?:регулярн\w*|коротк\w*\s+встреч))\b",
    ),
    "workload": (
        r"(?:нагруз|загруз|перегруз|параллельн\w*\s+задач)",
        r"\b(?:перегруз\w*|высок\w*(?:\s+нагруз\w*|\s+загруз\w*)|"
        r"(?:нагруз\w*|загруз\w*)\b.{0,25}"
        r"(?:высок\w*|выше\s+комфортн\w*|некомфортн\w*|"
        r"не\s+вернул\w*\s+к\s+комфортн\w*)|"
        r"(?:снизить|уменьшить)\s+(?:нагруз|загруз)\w*|"
        r"много\s+параллельн\w*\s+задач)\b",
    ),
    "team_interaction": (
        r"\bкоманд\w*\b",
        r"\b(?:конфликт\w*|напряжен\w*|некомфортн\w*|"
        r"не\s+получается\s+связаться|"
        r"(?:сложност\w*|проблем\w*|трудност\w*)\s+"
        r"(?:в\s+)?(?:общени\w*|взаимодействи\w*|коммуникаци\w*))\b",
    ),
}
_PATTERNS = {
    topic: (re.compile(anchor), re.compile(problem))
    for topic, (anchor, problem) in _TOPICS.items()
}


def issue_quotes(text: str) -> Iterator[tuple[str, str]]:
    """Yield one verbatim phrase per detected theme in a comment."""
    found: set[str] = set()
    for clause in _CLAUSE_BOUNDARY.split(text):
        quote = clause.strip(" \t\r,")
        normalized = quote.casefold().replace("ё", "е")
        if _NO_ISSUE.search(normalized):
            continue
        recovery = _RECOVERY.search(normalized)
        if recovery and not normalized[:recovery.start()].endswith("не "):
            continue
        for topic, (anchor, problem) in _PATTERNS.items():
            if topic in found or not anchor.search(normalized) or not problem.search(normalized):
                continue
            if topic == "workload" and _NEGATED_HIGH_LOAD.search(normalized):
                continue
            found.add(topic)
            yield topic, quote
