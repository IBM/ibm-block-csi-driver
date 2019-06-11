#!/bin/bash

docker kill $1
docker rm $1
docker kill $2
docker rm $2
docket network rm testnet