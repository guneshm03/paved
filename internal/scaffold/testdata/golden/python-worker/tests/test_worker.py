from worker import handle


def test_handle_acknowledges():
    assert handle({"id": 1})["status"] == "processed"
