/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"os"

	freebsdv1 "github.com/xaque208/freebsd_controller/api/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var poudriereLabelGate map[string]string = map[string]string{"poudriere.freebsd.znet/builder": "true"}

// nodeLabelMatch returns an error if not all of the matched key/value pairs are not matched against the given nodes labels.
func nodeLabelMatch(ctx context.Context, r client.Reader, req ctrl.Request, matchers map[string]string) error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	nodeName := types.NamespacedName{
		Namespace: req.Namespace,
		Name:      hostname,
	}

	var node freebsdv1.FreeBSDNode
	if err := r.Get(ctx, nodeName, &node); err != nil {
		return err
	}

	matchAll := func(labels, matchers map[string]string) bool {
		for k, v := range matchers {
			if val, ok := node.Labels[k]; ok {
				if val != v {
					return false
				}
			} else {
				return false
			}
		}

		return true
	}

	if matchAll(node.Labels, matchers) {
		return nil
	}

	return fmt.Errorf("unmatched labels: %s to matchers: %s", node.Labels, matchers)
}
