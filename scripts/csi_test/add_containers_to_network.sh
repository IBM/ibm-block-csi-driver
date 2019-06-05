#!/bin/bash



docker network create testnet


a=`docker ps -f "name=$1" | wc -l`
while [ a != '2' ]
do
    sleep 2
    echo "docker ps -f \"name=$1\" | wc -l"
    a=`docker ps -f "name=$1" | wc -l`
done


docker network connect testnet $1 --alias server 
echo "docker network connect testnet $1 --alias server"

a=`docker ps -f "name=$2" | wc -l`
while [ a != '2' ]
do
    sleep 2
    echo "docker ps -f \"name=$2\" | wc -l"
    a=`docker ps -f "name=$2" | wc -l`
done


docker network connect testnet $2 --alias client
echo "docker network connect testnet $2 --alias client"
