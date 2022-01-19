package loadbalancer

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	libovsdbclient "github.com/ovn-org/libovsdb/client"
	libovsdb "github.com/ovn-org/libovsdb/ovsdb"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/libovsdbops"
	"github.com/ovn-org/ovn-kubernetes/go-controller/pkg/nbdb"

	"k8s.io/klog/v2"
)

// EnsureLBs provides a generic load-balancer reconciliation engine.
//
// It assures that, for a given set of ExternalIDs, only the configured
// list of load balancers exist. Existing load-balancers will be updated,
// new ones will be created as needed, and stale ones will be deleted.
//
// For example, you might want to ensure that service ns/foo has the
// correct set of load balancers. You would call it with something like
//
//     EnsureLBs( { kind: Service, owner: ns/foo}, { {Name: Service_ns/foo_cluster_tcp, ...}})
//
// This will ensure that, for this example, only that one LB exists and
// has the desired configuration.
//
// It will commit all updates in a single transaction, so updates will be
// atomic and users should see no disruption. However, concurrent calls
// that modify the same externalIDs are not allowed.
//
// It is assumed that names are meaningful and somewhat stable, to minimize churn. This
// function doesn't work with Load_Balancers without a name.
func EnsureLBs(nbClient libovsdbclient.Client, externalIDs map[string]string, LBs []LB) error {
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished EnsureLBs: %v", time.Since(startTime))
	}()
	existing := libovsdbops.FindLoadBalancersByExternalIDs(nbClient, externalIDs)
	existingByName := make(map[string]nbdb.LoadBalancer, len(existing))
	toDelete := sets.NewString()

	// gather some data
	lbToSwitchNames, switchNamesToUUID, err := listSwitches(nbClient)
	if err != nil {
		return err
	}
	lbToRouterNames, routerNamesToUUID, err := listRouters(nbClient)
	if err != nil {
		return err
	}
	lbToGroupNames, groupNamesToUUID, err := listGroups(nbClient)
	if err != nil {
		return err
	}

	for _, lb := range existing {
		// collision - somehow things didn't come together how we expected
		// rare but we have seen it happen, see https://bugzilla.redhat.com/show_bug.cgi?id=2042001
		if _, ok := existingByName[lb.Name]; ok {
			klog.V(2).Infof("Name collision for load balancer %s: deleting both and re-creating")
			toDelete.Insert(lb.UUID)
			delete(existingByName, lb.Name)
			continue
		}
		toDelete.Insert(lb.UUID)
		existingByName[lb.Name] = lb
	}

	newlbs := make([]*nbdb.LoadBalancer, 0, len(LBs))
	existinglbs := make([]*nbdb.LoadBalancer, 0, len(LBs))

	addLBsToSwitch := map[string][]*nbdb.LoadBalancer{}
	removeLBsFromSwitch := map[string][]*nbdb.LoadBalancer{}
	addLBsToRouter := map[string][]*nbdb.LoadBalancer{}
	removesLBsFromRouter := map[string][]*nbdb.LoadBalancer{}
	addLBsToGroups := map[string][]*nbdb.LoadBalancer{}
	removeLBsFromGroups := map[string][]*nbdb.LoadBalancer{}

	for _, lb := range LBs {
		blb := buildLB(&lb)
		existingLB, exists := existingByName[lb.Name]
		existingRouters := sets.String{}
		existingSwitches := sets.String{}
		existingGroups := sets.String{}
		if exists {
			blb.UUID = existingLB.UUID
			existinglbs = append(existinglbs, blb)
			toDelete.Delete(existingLB.UUID)
			existingRouters = lbToRouterNames[existingLB.UUID]
			existingSwitches = lbToSwitchNames[existingLB.UUID]
			existingGroups = lbToGroupNames[existingLB.UUID]
		} else {
			newlbs = append(newlbs, blb)
		}
		wantRouters := sets.NewString(lb.Routers...)
		wantSwitches := sets.NewString(lb.Switches...)
		wantGroups := sets.NewString(lb.Groups...)
		mapLBDifferenceByKey(addLBsToSwitch, wantSwitches, existingSwitches, blb)
		mapLBDifferenceByKey(removeLBsFromSwitch, existingSwitches, wantSwitches, blb)
		mapLBDifferenceByKey(addLBsToRouter, wantRouters, existingRouters, blb)
		mapLBDifferenceByKey(removesLBsFromRouter, existingRouters, wantRouters, blb)
		mapLBDifferenceByKey(addLBsToGroups, wantGroups, existingGroups, blb)
		mapLBDifferenceByKey(removeLBsFromGroups, existingGroups, wantGroups, blb)
	}

	ops, err := libovsdbops.CreateOrUpdateLoadBalancersOps(nbClient, nil, existinglbs...)
	if err != nil {
		return err
	}

	ops, err = libovsdbops.CreateLoadBalancersOps(nbClient, ops, newlbs...)
	if err != nil {
		return err
	}

	// cache switches for this round of ops
	lswitches := map[string]*nbdb.LogicalSwitch{}
	getSwitch := func(name string) *nbdb.LogicalSwitch {
		var lswitch *nbdb.LogicalSwitch
		var found bool
		if lswitch, found = lswitches[name]; !found {
			lswitch = &nbdb.LogicalSwitch{Name: name, UUID: switchNamesToUUID[name]}
			lswitches[name] = lswitch
		}
		return lswitch
	}
	for k, v := range addLBsToSwitch {
		ops, err = libovsdbops.AddLoadBalancersToSwitchOps(nbClient, ops, getSwitch(k), v...)
		if err != nil {
			return err
		}
	}
	for k, v := range removeLBsFromSwitch {
		ops, err = libovsdbops.RemoveLoadBalancersFromSwitchOps(nbClient, ops, getSwitch(k), v...)
		if err != nil {
			return err
		}
	}

	// cache routers for this round of ops
	routers := map[string]*nbdb.LogicalRouter{}
	getRouter := func(name string) *nbdb.LogicalRouter {
		var router *nbdb.LogicalRouter
		var found bool
		if router, found = routers[name]; !found {
			router = &nbdb.LogicalRouter{Name: name, UUID: routerNamesToUUID[name]}
			routers[name] = router
		}
		return router
	}
	for k, v := range addLBsToRouter {
		ops, err = libovsdbops.AddLoadBalancersToRouterOps(nbClient, ops, getRouter(k), v...)
		if err != nil {
			return err
		}
	}
	for k, v := range removesLBsFromRouter {
		ops, err = libovsdbops.RemoveLoadBalancersFromRouterOps(nbClient, ops, getRouter(k), v...)
		if err != nil {
			return err
		}
	}

	// cache groups for this round of ops
	groups := map[string]*nbdb.LoadBalancerGroup{}
	getGroup := func(name string) *nbdb.LoadBalancerGroup {
		var group *nbdb.LoadBalancerGroup
		var found bool
		if group, found = groups[name]; !found {
			group = &nbdb.LoadBalancerGroup{Name: name, UUID: groupNamesToUUID[name]}
			groups[name] = group
		}
		return group
	}
	for k, v := range addLBsToGroups {
		ops, err = libovsdbops.AddLoadBalancersToGroupOps(nbClient, ops, getGroup(k), v...)
		if err != nil {
			return err
		}
	}
	for k, v := range removeLBsFromGroups {
		ops, err = libovsdbops.RemoveLoadBalancersFromGroupOps(nbClient, ops, getGroup(k), v...)
		if err != nil {
			return err
		}
	}

	deleteLBs := make([]*nbdb.LoadBalancer, 0, len(toDelete))
	for uuid := range toDelete {
		deleteLBs = append(deleteLBs, &nbdb.LoadBalancer{UUID: uuid})
	}
	ops, err = libovsdbops.DeleteLoadBalancersOps(nbClient, ops, deleteLBs...)
	if err != nil {
		return err
	}

	_, err = libovsdbops.TransactAndCheck(nbClient, ops)
	if err != nil {
		return err
	}

	klog.V(5).Infof("Deleted %d stale LBs for %#v", len(toDelete), externalIDs)

	return nil
}

// LoadBalancersEqualNoUUID compares load balancer objects excluding uuid
func LoadBalancersEqualNoUUID(lbs1, lbs2 []LB) bool {
	if len(lbs1) != len(lbs2) {
		return false
	}
	new1 := make([]LB, len(lbs1))
	new2 := make([]LB, len(lbs2))
	for _, lb := range lbs1 {
		lb.UUID = ""
		new1 = append(new1, lb)

	}
	for _, lb := range lbs2 {
		lb.UUID = ""
		new2 = append(new2, lb)
	}
	return reflect.DeepEqual(new1, new2)
}

func mapLBDifferenceByKey(keyMap map[string][]*nbdb.LoadBalancer, keyIn sets.String, keyNotIn sets.String, lb *nbdb.LoadBalancer) {
	for _, k := range keyIn.Difference(keyNotIn).UnsortedList() {
		l := keyMap[k]
		if l == nil {
			l = []*nbdb.LoadBalancer{}
		}
		l = append(l, lb)
		keyMap[k] = l
	}
}

func buildLB(lb *LB) *nbdb.LoadBalancer {
	reject := "true"
	event := "false"

	if lb.Opts.Unidling {
		reject = "false"
		event = "true"
	}

	skipSNAT := "false"
	if lb.Opts.SkipSNAT {
		skipSNAT = "true"
	}

	options := map[string]string{
		"reject":    reject,
		"event":     event,
		"skip_snat": skipSNAT,
	}

	// Session affinity
	// If enabled, then bucket flows by 3-tuple (proto, srcip, dstip)
	// otherwise, use default ovn value
	selectionFields := []nbdb.LoadBalancerSelectionFields{}
	if lb.Opts.Affinity {
		selectionFields = []string{
			nbdb.LoadBalancerSelectionFieldsIPSrc,
			nbdb.LoadBalancerSelectionFieldsIPDst,
		}
	}

	// vipMap
	vips := buildVipMap(lb.Rules)

	return libovsdbops.BuildLoadBalancer(lb.Name, strings.ToLower(lb.Protocol), selectionFields, vips, options, lb.ExternalIDs)
}

// buildVipMap returns a viups map from a set of rules
func buildVipMap(rules []LBRule) map[string]string {
	vipMap := make(map[string]string, len(rules))
	for _, r := range rules {
		tgts := make([]string, 0, len(r.Targets))
		for _, tgt := range r.Targets {
			tgts = append(tgts, tgt.String())
		}
		vipMap[r.Source.String()] = strings.Join(tgts, ",")
	}

	return vipMap
}

// DeleteLBs deletes all load balancer uuids supplied
// Note: this also automatically removes them from the switches, routers, and the groups :-)
func DeleteLBs(nbClient libovsdbclient.Client, uuids []string) error {
	if len(uuids) == 0 {
		return nil
	}

	lbs := make([]*nbdb.LoadBalancer, 0, len(uuids))
	for _, uuid := range uuids {
		lbs = append(lbs, &nbdb.LoadBalancer{UUID: uuid})
	}

	err := libovsdbops.DeleteLoadBalancers(nbClient, lbs)
	if err != nil {
		return err
	}

	return nil
}

type DeleteVIPEntry struct {
	LBUUID string
	VIPs   []string // ip:string (or v6 equivalent)
}

// DeleteLoadBalancerVIPs removes VIPs from load-balancers in a single shot.
func DeleteLoadBalancerVIPs(nbClient libovsdbclient.Client, toRemove []DeleteVIPEntry) error {
	var ops []libovsdb.Operation
	var err error
	for _, entry := range toRemove {
		ops, err = libovsdbops.RemoveLoadBalancerVipsOps(nbClient, ops, &nbdb.LoadBalancer{UUID: entry.LBUUID}, entry.VIPs...)
		if err != nil {
			return err
		}
	}

	_, err = libovsdbops.TransactAndCheck(nbClient, ops)
	if err != nil {
		return fmt.Errorf("failed to remove vips from load_balancer: %w", err)
	}

	return nil
}

// listSwitches builds up the lists we need to reconcile the lb -> switch mapping
// returns:
// - map of lb UUID to switch names
// - map of switch name to switch uuid
func listSwitches(nbClient libovsdbclient.Client) (lbToSwitches map[string]sets.String, nameToUUID map[string]string, err error) {
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished listSwitches: %v", time.Since(startTime))
	}()
	switches, err := libovsdbops.ListSwitchesWithLoadBalancers(nbClient)
	if err != nil {
		return
	}

	lbToSwitches = map[string]sets.String{}
	nameToUUID = map[string]string{}

	for _, swtch := range switches {
		if swtch.Name == "" {
			continue
		}
		nameToUUID[swtch.Name] = swtch.UUID
		for _, lbUUID := range swtch.LoadBalancer {
			if _, ok := lbToSwitches[lbUUID]; !ok {
				lbToSwitches[lbUUID] = sets.NewString()
			}

			lbToSwitches[lbUUID].Insert(swtch.Name)
		}
	}

	return
}

// listRouters builds up the lists we need to reconcile the lb -> router mapping
// returns:
// - map of lb UUID to router names
// - map of router name to router uuid
func listRouters(nbClient libovsdbclient.Client) (lbToRouters map[string]sets.String, nameToUUID map[string]string, err error) {
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished listSwitches: %v", time.Since(startTime))
	}()
	routers, err := libovsdbops.ListRoutersWithLoadBalancers(nbClient)
	if err != nil {
		return
	}

	lbToRouters = map[string]sets.String{}
	nameToUUID = map[string]string{}

	for _, router := range routers {
		if router.Name == "" {
			continue
		}
		nameToUUID[router.Name] = router.UUID
		for _, lbUUID := range router.LoadBalancer {
			if _, ok := lbToRouters[lbUUID]; !ok {
				lbToRouters[lbUUID] = sets.NewString()
			}

			lbToRouters[lbUUID].Insert(router.Name)
		}
	}

	return
}

// listGroups builds up the lists we need to reconcile the lb -> group mapping
// returns:
// - map of lb UUID to group names
// - map of group name to group uuid
func listGroups(nbClient libovsdbclient.Client) (lbToGroups map[string]sets.String, nameToUUID map[string]string, err error) {
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished listSwitches: %v", time.Since(startTime))
	}()
	groups, err := libovsdbops.ListGroupsWithLoadBalancers(nbClient)
	if err != nil {
		return
	}

	lbToGroups = map[string]sets.String{}
	nameToUUID = map[string]string{}

	for _, group := range groups {
		if group.Name == "" {
			continue
		}
		nameToUUID[group.Name] = group.UUID
		for _, lbUUID := range group.LoadBalancer {
			if _, ok := lbToGroups[lbUUID]; !ok {
				lbToGroups[lbUUID] = sets.NewString()
			}

			lbToGroups[lbUUID].Insert(group.Name)
		}
	}

	return
}
