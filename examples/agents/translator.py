#!/usr/bin/env python3
"""
Translation service agent for Alancoin.

A realistic example: translates text between languages.
Uses a simple dictionary for the demo — swap in any translation API
(DeepL, Google, OpenAI) for production.

Usage:
    # Start the Alancoin platform first (make run), then:
    python examples/agents/translator.py
"""
import sys
import os

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "sdks", "python"))

from alancoin.serve import ServiceAgent

# Simple word-level translation tables (demo only)
TRANSLATIONS = {
    "es": {
        "hello": "hola",
        "world": "mundo",
        "good": "bueno",
        "morning": "manana",
        "thank": "gracias",
        "you": "tu",
        "how": "como",
        "are": "estas",
        "the": "el",
        "is": "es",
        "a": "un",
        "of": "de",
        "and": "y",
        "to": "a",
        "in": "en",
        "for": "para",
        "it": "ello",
        "with": "con",
        "this": "esto",
        "that": "eso",
        "i": "yo",
        "my": "mi",
        "your": "tu",
        "we": "nosotros",
        "they": "ellos",
    },
    "fr": {
        "hello": "bonjour",
        "world": "monde",
        "good": "bon",
        "morning": "matin",
        "thank": "merci",
        "you": "vous",
        "how": "comment",
        "are": "etes",
        "the": "le",
        "is": "est",
        "a": "un",
        "of": "de",
        "and": "et",
        "to": "a",
        "in": "dans",
        "for": "pour",
        "it": "il",
        "with": "avec",
        "this": "ceci",
        "that": "cela",
        "i": "je",
        "my": "mon",
        "your": "votre",
        "we": "nous",
        "they": "ils",
    },
    "de": {
        "hello": "hallo",
        "world": "welt",
        "good": "gut",
        "morning": "morgen",
        "thank": "danke",
        "you": "du",
        "how": "wie",
        "are": "bist",
        "the": "das",
        "is": "ist",
        "a": "ein",
        "of": "von",
        "and": "und",
        "to": "zu",
        "in": "in",
        "for": "fur",
        "it": "es",
        "with": "mit",
        "this": "dies",
        "that": "das",
        "i": "ich",
        "my": "mein",
        "your": "dein",
        "we": "wir",
        "they": "sie",
    },
}

LANGUAGE_NAMES = {"es": "Spanish", "fr": "French", "de": "German", "en": "English"}


def translate_text(text: str, target: str) -> str:
    """Word-level translation using lookup tables."""
    table = TRANSLATIONS.get(target, {})
    if not table:
        return text

    words = text.split()
    translated = []
    for word in words:
        clean = word.lower().strip(".,!?;:")
        punct = word[len(clean) :] if len(clean) < len(word) else ""
        result = table.get(clean, word)
        # Preserve original capitalization
        if word and word[0].isupper():
            result = result.capitalize()
        translated.append(result + punct)
    return " ".join(translated)


agent = ServiceAgent(
    name="TranslatorBot",
    description="Translates text between languages. Supports Spanish, French, and German.",
)


@agent.service("translation", price="0.005", description="Translate text to another language")
def translate(text="", target="es"):
    target_name = LANGUAGE_NAMES.get(target, target)
    output = translate_text(text, target)
    return {
        "output": output,
        "source_language": "en",
        "target_language": target,
        "target_name": target_name,
        "word_count": len(text.split()),
    }


@agent.service("detect_language", price="0.002", description="Detect the language of text")
def detect_language(text=""):
    # Simple heuristic for demo
    lower = text.lower()
    scores = {}
    for lang, table in TRANSLATIONS.items():
        matches = sum(1 for word in lower.split() if word.strip(".,!?;:") in table.values())
        scores[lang] = matches
    scores["en"] = sum(
        1 for word in lower.split()
        if any(word.strip(".,!?;:") in table for table in TRANSLATIONS.values())
    )

    best = max(scores, key=scores.get) if any(scores.values()) else "en"
    return {
        "detected_language": best,
        "language_name": LANGUAGE_NAMES.get(best, best),
        "confidence": min(scores.get(best, 0) / max(len(text.split()), 1), 1.0),
    }


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "5002"))
    agent.serve(port=port)
