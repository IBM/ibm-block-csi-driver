import math


# =============================================================================
# Definitions
# =============================================================================

PiB = 2**50     # Pebibyte = PiB = 2^50 B = 1,125,899,906,842,624 bytes
TiB = 2**40     # Tebibyte = TiB = 2^40 B = 10,995,116,277,761 bytes
GiB = 2**30     # Gibibyte = GiB = 2^30 B = 1,073,741,824 bytes
MiB = 2**20     # Mebibyte = MiB = 2^20 B = 1,048,576 bytes
KiB = 2**10     # Kibibyte = kiB = 2^10 B = 1,024 bytes

GB = 10**9      # Gigabyte = GB = 10^9 B = 1,000,000,000 bytes
MB = 10**6      # Megabyte = MB = 10^6 B = 1,000,000 bytes
KB = 10**3      # Kilobyte = kB = 10^3 B = 1,000 bytes


# =============================================================================
# Methods converting to bytes.
# =============================================================================

def convert_size_gib_to_bytes(size_in_gib):
    return size_in_gib * GiB


# =============================================================================
# Methods converting from bytes.
# =============================================================================

def convert_size_bytes_to_gib(size_in_bytes):
    return float(size_in_bytes) / GiB


def convert_and_ceil_size_bytes_to_gib(size_in_bytes):
    return int(math.ceil(convert_size_bytes_to_gib(size_in_bytes)))
