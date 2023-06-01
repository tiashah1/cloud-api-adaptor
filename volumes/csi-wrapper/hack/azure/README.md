# csi-wrapper
Azure File CSI Wrapper for Peer Pod Storage

## High Level Design

![design](../../images/csi-wrapper.png)

> **Note** Edited via https://excalidraw.com/

## Test CSI Wrapper with azurefiles-csi-driver for peer pod demo on Azure Cloud

### Set up a demo environment on your development machine

1. Follow the [README.md](../../../../azure/README.md) to setup a x86 based demo environment on Azure Cloud.

2. To prevent our changes to be rolled back, disable the built-in AKS azurefile driver
```bash
az aks update -g ${AZURE_RESOURCE_GROUP} --name ${CLUSTER_NAME} --disable-file-driver
```

3. Assign the "Storage Account Contributor" role to the AKS agentpool application so it can create storage accounts:

```bash
OBJECT_ID="$(az ad sp list --display-name "${CLUSTER_NAME}-agentpool" --query '[].id' --output tsv)"
az role assignment create --role "Storage Account Contributor" --assignee-object-id ${OBJECT_ID} --assignee-principal-type ServicePrincipal --scope /subscriptions/d1aa957b-94f5-49ef-b29a-0178c58a7132/resourceGroups/iago-testing-aks
```

### Deploy azurefile-csi-driver on the cluster
Note: All the steps can be performed anywhere with cluster access

1. Clone the azurefile-csi-driver source:
```bash
git clone --depth 1 --branch v1.24 https://github.com/kubernetes-sigs/azurefile-csi-driver
cd azurefile-csi-driver
```

2. Enable `attachRequired` in the CSI Driver:
```bash
sed -i 's/attachRequired: false/attachRequired: true/g' deploy/csi-azurefile-driver.yaml
```

3. Run the script:
```bash
bash ./deploy/install-driver.sh master local
```

### Build custom csi-wrapper images (for development)
Follow this if you have made changes to the CSI wrapper code and want to deploy those changes.

1. Go back to the cloud-api-adaptor directory
```bash
cd ~/cloud-api-adaptor
```

2. Build csi-wrapper images:
```bash
cd volumes/csi-wrapper/
make csi-controller-wrapper-docker
make csi-node-wrapper-docker
make csi-podvm-wrapper-docker
cd -
```

3. Export custom registry

```bash
export REGISTRY="my-registry" # e.g. "quay.io/my-registry"
```

4. Tag and push images
```bash
docker tag docker.io/library/csi-controller-wrapper:local ${REGISTRY}/csi-controller-wrapper:latest
docker tag docker.io/library/csi-node-wrapper:local ${REGISTRY}/csi-node-wrapper:latest
docker tag docker.io/library/csi-podvm-wrapper:local ${REGISTRY}/csi-podvm-wrapper:latest

docker push ${REGISTRY}/csi-controller-wrapper:latest
docker push ${REGISTRY}/csi-node-wrapper:latest
docker push ${REGISTRY}/csi-podvm-wrapper:latest
```

5. Change image in CSI wrapper k8s resources
```bash
sed -i "s#quay.io/confidential-containers#${REGISTRY}#g" volumes/csi-wrapper/hack/azure/*
```

### Deploy csi-wrapper to patch azurefiles-csi-driver

1. Go back to the cloud-api-adaptor directory
```bash
cd ~/cloud-api-adaptor
```

2. Create the PeerpodVolume CRD object
```bash
kubectl apply -f volumes/csi-wrapper/crd/peerpodvolume.yaml
```

The output looks like:
```bash
customresourcedefinition.apiextensions.k8s.io/peerpodvolumes.peerpod.azure.com created
```

3. Configure RBAC so that the wrapper has access to the required operations
```bash
kubectl apply -f volumes/csi-wrapper/hack/azure/azure-files-csi-wrapper-runner.yaml
```

4. patch csi-azurefile-driver:
```bash
kubectl patch deploy csi-azurefile-controller -n kube-system --patch-file volumes/csi-wrapper/hack/azure/patch-controller.yaml
kubectl -n kube-system delete replicaset -l app=csi-azurefile-controller
kubectl patch ds csi-azurefile-node -n kube-system --patch-file volumes/csi-wrapper/hack/azure/patch-node.yaml
```

5. Create **storage class**:
```bash
kubectl apply -f volumes/csi-wrapper/hack/azure/azure-file-StorageClass-for-peerpod.yaml
```

## Run the `csi-wrapper for peerpod storage` demo

1. Create one pvc that use `azurefiles-csi-driver`
```bash
kubectl apply -f volumes/csi-wrapper/hack/azure/my-pvc-kube-system.yaml
```

2. Wait for the pvc status to become `bound`
```bash
3:54:25.693 [root@kartik-ThinkPad-X1-Titanium-Gen-1 csi-wrapper]# k get pvc -A
NAMESPACE     NAME            STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS         AGE
kube-system   pvc-azurefile   Bound    pvc-699ecee6-56f4-4a9e-9de6-72320c475504   1Gi        RWO            azure-file-storage   11h
```

3. Create the nginx peer-pod demo with with `podvm-wrapper` and `azurefile-csi-driver` containers
```bash
kubectl apply -f volumes/csi-wrapper/hack/azure/nginx-kata-with-my-pvc-and-csi-wrapper.yaml
```

4. Exec into the container and check the mount

```bash
kubectl exec nginx-pv -n kube-system -c nginx -i -t -- sh
# mount | grep mount-path
//fffffffffffffffffffffff.file.core.windows.net/pvc-ff587660-73ed-4bd0-8850-285be480f490 on /mount-path type cifs (rw,relatime,vers=3.1.1,cache=strict,username=fffffffffffffffffffffff,uid=0,noforceuid,gid=0,noforcegid,addr=x.x.x.x,file_mode=0777,dir_mode=0777,soft,persistenthandles,nounix,serverino,mapposix,mfsymlinks,rsize=1048576,wsize=1048576,bsize=1048576,echo_interval=60,actimeo=30,closetimeo=1)
```

> **Note** We can see there's a CIFS mount to `/mount-path` as expected
