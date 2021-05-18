from dataclasses import dataclass


@dataclass
class Secret:
    user: str
    password: str
    array_addresses: list
    uid: str
