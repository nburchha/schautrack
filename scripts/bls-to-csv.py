#!/usr/bin/env -S uv run --script
# /// script
# dependencies = ["openpyxl"]
# ///
"""Converts the BLS 4.0 Excel file into the slim CSV the importer embeds.

    uv run scripts/bls-to-csv.py BLS_4_0_Daten_2025_DE.xlsx internal/foodimport/bls/bls_4_0.csv

Source: https://blsdb.de/download (Max Rubner-Institut, CC BY 4.0).
Run again when MRI publishes a new version, then commit the CSV.

A missing value stays empty (NULL on import), never 0. BLS marks gaps with "-".
"TR" (trace) and "<LOD"/"<LOQ" (below detection limit) are measured but
negligible amounts and become 0.
"""
import csv
import sys

import openpyxl

# Header prefix -> CSV column. All values are per 100 g edible portion.
NUTRIENTS = [
    ("ENERCC", "kcal"),
    ("PROT625", "protein"),
    ("CHO", "carbs"),
    ("FAT", "fat"),
    ("FIBT", "fiber"),
    ("SUGAR", "sugar"),
]
TRACE = {"TR", "<LOD", "<LOQ", "<LOD or <LOQ"}


def convert(value):
    if value is None:
        return ""
    if isinstance(value, (int, float)):
        return f"{value:.4g}" if value != int(value) else str(int(value))
    text = str(value).strip()
    if text in TRACE:
        return "0"
    if text in ("", "-"):
        return ""
    return str(float(text.replace(",", ".")))


def main(src, dst):
    ws = openpyxl.load_workbook(src, read_only=True).active
    rows = ws.iter_rows(values_only=True)
    header = next(rows)
    col = {}
    for prefix, name in NUTRIENTS:
        col[name] = next(i for i, h in enumerate(header) if str(h).startswith(prefix + " "))

    seen = set()
    with open(dst, "w", newline="", encoding="utf-8") as f:
        out = csv.writer(f, lineterminator="\n")
        out.writerow(["code", "name", "classification", *[n for _, n in NUTRIENTS]])
        for row in rows:
            code = row[0]
            if not code:
                continue
            if code in seen:
                raise SystemExit(f"duplicate BLS code {code}")
            seen.add(code)
            # The first letter of a BLS code is its main food group.
            out.writerow([code, row[1], code[0], *[convert(row[col[n]]) for _, n in NUTRIENTS]])
    print(f"{len(seen)} foods written to {dst}")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    main(sys.argv[1], sys.argv[2])
