"""Audit Worker: consumes `audit.events` with 8 workers."""

TOPIC = "audit.events"
CONCURRENCY = 8


def handle(event: dict) -> dict:
    """Process one event; return the acknowledgement payload."""
    return {"topic": TOPIC, "id": event.get("id"), "status": "processed"}
