#!/bin/bash
set -x
[ -n "$1" ] && coverage="-v $1:/driver/coverage:z"
