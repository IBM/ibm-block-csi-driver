#!/bin/bash

docker network create testnet

docker network connect testnet $1 --alias server 
echo "docker network connect testnet $1 --alias server"
docker network connect testnet $2 --alias client 
echo "docker network connect testnet $2 --alias client"
