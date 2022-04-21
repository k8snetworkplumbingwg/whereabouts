package entities

import (
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const testImage = "quay.io/dougbtv/alpine:latest"

func PodObject(podName string, namespace string, label, annotations map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: podMeta(podName, namespace, label, annotations),
		Spec:       podSpec("samplepod"),
	}
}

func podSpec(containerName string) corev1.PodSpec {
	return corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:    containerName,
				Command: containerCmd(),
				Image:   testImage,
			},
		},
	}
}

func StatefulSetSpec(statefulSetName string, namespace string, serviceName string, replicaNumber int, annotations map[string]string) *v1.StatefulSet {
	const labelKey = "app"

	replicas := int32(replicaNumber)
	webAppLabels := map[string]string{labelKey: serviceName}
	return &v1.StatefulSet{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{Name: serviceName},
		Spec: v1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: webAppLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: podMeta(statefulSetName, namespace, webAppLabels, annotations),
				Spec:       podSpec(statefulSetName),
			},
			ServiceName:         serviceName,
			PodManagementPolicy: v1.ParallelPodManagement,
		},
	}
}

func ReplicaSetObject(replicaCount int32, rsName string, namespace string, label map[string]string, annotations map[string]string) *v1.ReplicaSet {
	numReplicas := &replicaCount

	const podName = "samplepod"
	return &v1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ReplicaSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rsName,
			Namespace: namespace,
			Labels:    label,
		},
		Spec: v1.ReplicaSetSpec{
			Replicas: numReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: label,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      label,
					Annotations: annotations,
					Namespace:   namespace,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    podName,
							Command: containerCmd(),
							Image:   testImage,
						},
					},
				},
			},
		},
	}
}

func podMeta(podName string, namespace string, label map[string]string, annotations map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:        podName,
		Namespace:   namespace,
		Labels:      label,
		Annotations: annotations,
	}
}

func containerCmd() []string {
	return []string{"/bin/ash", "-c", "trap : TERM INT; sleep infinity & wait"}
}
