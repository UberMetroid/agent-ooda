"""ooda / router command-line interface package."""

from .build_args import build_parser
from .run_cli import main

__all__ = ["build_parser", "main"]
