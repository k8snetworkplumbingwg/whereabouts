#!/bin/bash

# Copyright 2021 Nokia
# Licensed under the Apache License 2.0.
# SPDX-License-Identifier: Apache-2.0

#set -o errexit
set -o pipefail

while true
do
  date
  # cache Pod IP usage
  podipmap=`kubectl get pods -A --ignore-not-found -o=jsonpath='{.items[?(@.metadata.annotations.k8s\.v1\.cni\.cncf\.io/networks-status)].metadata}' | jq -r '(.namespace + " " + .name + " " + (.annotations."k8s.v1.cni.cncf.io/networks-status" | fromjson | .[] | select(.name != "") | .ips | join(",") ))'`

  while read ippool
  do
    base=`kubectl get "$ippool" -n kube-system --ignore-not-found -o=jsonpath='{.spec.range}' | cut -d'/' -f1`
    if [[ -z "$base" ]]; then continue; fi
    while read index podref
    do
      ip=`python -c "import ipaddress; print(ipaddress.ip_address(u'$base') + $index);"`
      if [[ -z "$podref" ]]
      then
        while IFS= read -r line
        do
          echo $line | grep $ip
          if [ $? == 0 ]
          then
             echo " ** WARNING **"
             echo "-> pod $line has no podref"
          fi
        done <<< "$podipmap"
        continue
      fi
      found=0
      ns=`echo $podref | cut -d'/' -f1`
      podname=`echo $podref | cut -d'/' -f2`

      # check whether the referenced Pod really owns that IP, otherwise the IP is released
      podips=`kubectl get pod -n $ns $podname --ignore-not-found -o=jsonpath='{.metadata.annotations.k8s\.v1\.cni\.cncf\.io/networks-status}' | jq -r '.[].ips[]'`
      if [[ -n "$podips" ]]
      then
        for podip in $podips
        do
          if [[ $ip == $podip ]]; then found=1; break; fi
        done
      fi
      if [[ $found == 0 ]]
      then
        stspod=`kubectl get -n $ns pod $podname --ignore-not-found -o=jsonpath='{.metadata.labels}' | jq -r '."statefulset.kubernetes.io/pod-name"'`
        if [[ $stspod == $podname ]]
        then
          echo "$podref is a statefulset, skip removing IP allocation"
        else
          echo "-> Pod not found -> removing IP allocation"
          kubectl patch "$ippool" -n kube-system --type=merge -p "{\"spec\":{\"allocations\":{\"$index\":null}}}"
          continue
        fi
      fi

      # check whether the allocated IP is used by any non-referenced Pods (e.g. multiple Pods use the same IP) -> non-referenced Pods need to be deleted
      duppods=`echo "$podipmap" | egrep "[^[:alnum:]]$ip([^[:alnum:]]|$)" || true`
      if [[ -z "$duppods" ]]; then continue; fi
      dupfound=0
      while read dupns duppodname dupip
      do
        if [[ "$dupns" != "$ns" || "$duppodname" != "$podname" && "$dupip" =~ "$ip" ]]
        then
          dupfound=1
          echo " ** WARNING **"
          echo "-> Multiple pods are found for IP $ip in $ippool"
          echo "-> registered pod: $ns/$podname"
          echo "-> duplicate pod: $dupns/$duppodname"
	  dupstspod=`kubectl get -n $dupns pod $duppodname --ignore-not-found -o=jsonpath='{.metadata.labels}' | jq -r '."statefulset.kubernetes.io/pod-name"'`
	  if [[  $dupstspod != $duppodname ]]
          then
            echo "-> duplicate pod $dupns/$duppodname is not statefulset -> deleting"
	    kubectl delete pod -n "$dupns" "$duppodname" --ignore-not-found
          fi          
        fi
      done <<< "$duppods"
      if [[ $dupfound == 1 ]]; then break 2; fi

    done < <(kubectl get "$ippool" -n kube-system --ignore-not-found -o=jsonpath='{.spec.allocations}' | jq -r 'to_entries | .[] | .key + " " + .value.podref')
  done < <(kubectl get ippools -n kube-system --no-headers --ignore-not-found -o=name)

  sleep 45
done

