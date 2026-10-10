//go:build livewrite

package vngcloud_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/portal"
	"danny.vn/vngcloud/volume"
)

// liveEncryptionTypeID returns the ID of the encryption type whose string is
// want from ListEncryptionTypes, so a renamed or removed type stops the run
// before any order.
func liveEncryptionTypeID(ctx context.Context, t *testing.T, client *volume.Client, want string) string {
	t.Helper()
	types, err := client.ListEncryptionTypes(ctx, nil)
	if err != nil {
		t.Fatalf("ListEncryptionTypes: %s", safeErr(err))
	}
	for _, et := range types.Items {
		if et.ID == want {
			return et.ID
		}
	}
	t.Fatalf("encryption type %s is not listed (%d types)", want, len(types.Items))
	return ""
}

// liveCash reads the cash balance, or nil when it cannot be read; callers
// log amounts only and treat a missing balance as non-fatal.
func liveCash(ctx context.Context, t *testing.T, client *billing.Client) *float64 {
	t.Helper()
	out, err := client.GetBalances(ctx, &billing.GetBalancesInput{})
	if err != nil {
		t.Logf("GetBalances failed (non-fatal): %s", safeErr(err))
		return nil
	}
	return out.Balances.Cash
}

// logLiveCashDelta logs the cash balance change from before to now.
func logLiveCashDelta(ctx context.Context, t *testing.T, client *billing.Client, label string, before *float64) {
	t.Helper()
	after := liveCash(ctx, t, client)
	if before == nil || after == nil {
		t.Logf("%s: cash balance not readable", label)
		return
	}
	t.Logf("%s: cash change %.0f VND from the start", label, *after-*before)
}

// liveRefundToleranceVND is how far below its starting value the balance may
// stay once a delete's refund posts: a delete refunds to the minute, so the
// minutes the resource lived stay charged, about 12 VND a minute for the
// largest resource these tests order.
const liveRefundToleranceVND = 1000

// logLiveRefund polls the cash balance for up to 5 minutes until it is
// within liveRefundToleranceVND of before, since a delete's refund can post
// a little after the delete settles, and logs the final change and how long
// the refund took.
func logLiveRefund(ctx context.Context, t *testing.T, client *billing.Client, label string, before *float64) {
	t.Helper()
	if before == nil {
		t.Logf("%s: cash balance before was not readable", label)
		return
	}
	start := time.Now()
	for {
		after := liveCash(ctx, t, client)
		if after != nil && *before-*after <= liveRefundToleranceVND {
			t.Logf("%s: refund posted after %s, change %.0f VND from the start", label, time.Since(start).Round(time.Second), *after-*before)
			return
		}
		if time.Since(start) > 5*time.Minute || ctx.Err() != nil {
			change := 0.0
			if after != nil {
				change = *after - *before
			}
			t.Errorf("%s: refund not posted after %s, change %.0f VND from the start", label, time.Since(start).Round(time.Second), change)
			return
		}
		time.Sleep(10 * time.Second)
	}
}

func liveWriteConfig(ctx context.Context, t *testing.T) vngcloud.Config {
	t.Helper()
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithConfigFile(emptyWriteFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")),
	)
	if errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Fatal("set VNGCLOUD_ROOT_EMAIL, VNGCLOUD_USERNAME, and VNGCLOUD_PASSWORD (and optionally VNGCLOUD_TOTP_SECRET) in .env")
	}
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// TestLiveWritePaidEncryptedVolume orders one encrypted 10 GB SSD volume at
// its quote, reads the encryption type back, deletes it, and logs the cash
// balance before and after to show the refund. It is gated by
// VNGCLOUD_LIVE_WRITE=1, VNGCLOUD_LIVE_PAID_ENCRYPTED_VOLUME=1, and
// VNGCLOUD_LIVE_MAX_VND, and needs the owner's approval for the run. It
// shares the sweep of "vngcloud-live-" volumes with the other paid tests, so
// it never runs at the same time as one of them.
func TestLiveWritePaidEncryptedVolume(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live paid vServer write tests")
	}
	if os.Getenv("VNGCLOUD_LIVE_PAID_ENCRYPTED_VOLUME") != "1" {
		t.Skip("set VNGCLOUD_LIVE_PAID_ENCRYPTED_VOLUME=1 to run the live encrypted volume test; " +
			"it orders a real, billed volume and needs the owner's approval")
	}
	budgetCap := liveMonitorMaxVND(t)
	const zoneID = "HCM03-1C" // the test account's only enabled zone

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cfg := liveWriteConfig(ctx, t)
	computeClient := compute.New(cfg)
	volumeClient := volume.New(cfg)
	billingClient := billing.New(cfg)

	t.Logf("step 1: deleted %d leftover volume(s)", deleteLiveVolumes(ctx, t, volumeClient))

	typeID := liveEncryptionTypeID(ctx, t, volumeClient, "aes-xts-plain64_256")
	defaultType, err := volumeClient.GetDefaultVolumeType(ctx, &volume.GetDefaultVolumeTypeInput{ZoneID: zoneID})
	if err != nil {
		t.Fatalf("step 2 GetDefaultVolumeType: %s", safeErr(err))
	}
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	input := &volume.CreateVolumeInput{
		Name: "vngcloud-live-" + suffix, ZoneID: zoneID, Size: 10,
		VolumeTypeID: defaultType.VolumeType.ID, EncryptionTypeID: typeID,
	}

	quote, err := volumeClient.QuoteCreateVolume(ctx, input)
	if err != nil {
		t.Fatalf("step 2 QuoteCreateVolume: %s", safeErr(err))
	}
	t.Logf("step 2: quote %.0f VND a month", quote.OptimumPrice)
	if quote.OptimumPrice > budgetCap {
		t.Fatalf("step 2: quote %.0f VND exceeds this run's cap %.0f VND; ordering nothing", quote.OptimumPrice, budgetCap)
	}
	before := liveCash(ctx, t, billingClient)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		t.Logf("cleanup: deleted %d vngcloud-live volume(s)", deleteLiveVolumes(cleanupCtx, t, volumeClient))
		assertNoLiveServersOrVolumesRemain(cleanupCtx, t, computeClient, volumeClient)
	})

	input.MaxPrice = quote.OptimumPrice
	createStart := time.Now()
	created, err := volumeClient.CreateVolume(ctx, input)
	if err != nil {
		t.Fatalf("step 3 CreateVolume: %s", safeErr(err))
	}
	t.Logf("step 3: settled after %s at status %s", time.Since(createStart), created.Volume.Status)
	if created.Volume.Status != "AVAILABLE" {
		t.Fatalf("step 3: status = %s, want AVAILABLE", created.Volume.Status)
	}
	volumeID := created.Volume.UUID
	logLiveCashDelta(ctx, t, billingClient, "step 3 after create", before)

	read, err := volumeClient.GetVolume(ctx, &volume.GetVolumeInput{VolumeID: volumeID})
	if err != nil {
		t.Fatalf("step 4 GetVolume: %s", safeErr(err))
	}
	t.Logf("step 4: GetVolume carries encryptionType: %v, equal to the requested type: %v",
		read.Volume.EncryptionType != nil, read.Volume.EncryptionType != nil && *read.Volume.EncryptionType == typeID)
	under, err := volumeClient.GetUnderlyingVolume(ctx, &volume.GetUnderlyingVolumeInput{VolumeID: volumeID})
	if err != nil {
		t.Fatalf("step 4 GetUnderlyingVolume: %s", safeErr(err))
	}
	t.Logf("step 4: GetUnderlyingVolume carries encryptionType: %v, equal to the requested type: %v",
		under.Volume.EncryptionType != nil, under.Volume.EncryptionType != nil && *under.Volume.EncryptionType == typeID)

	deleteStart := time.Now()
	if _, err := volumeClient.DeleteVolume(ctx, &volume.DeleteVolumeInput{VolumeID: volumeID}); err != nil {
		t.Fatalf("step 5 DeleteVolume: %s", safeErr(err))
	}
	t.Logf("step 5: delete settled after %s", time.Since(deleteStart))
	if _, err := volumeClient.GetVolume(ctx, &volume.GetVolumeInput{VolumeID: volumeID}); !vngcloud.IsNotFound(err) {
		t.Fatalf("step 5: GetVolume after delete = %s, want NotFound", safeErr(err))
	}
	assertNoLiveServersOrVolumesRemain(ctx, t, computeClient, volumeClient)
	logLiveRefund(ctx, t, billingClient, "step 5 after delete", before)
}

// TestLiveWritePaidEncryptedServer orders one s2-general-1x2 server with a
// 20 GB encrypted root disk and a 20 GB encrypted data disk at its quote,
// reads the encryption type on each disk, creates a separate encrypted 10 GB
// volume and tries to attach it, then deletes everything and logs the cash
// balance before and after. It is gated by VNGCLOUD_LIVE_WRITE=1,
// VNGCLOUD_LIVE_PAID_ENCRYPTED_SERVER=1, and VNGCLOUD_LIVE_MAX_VND, which
// caps each quote separately; the server and the volume are both refunded on
// delete. It needs the owner's approval for the run.
func TestLiveWritePaidEncryptedServer(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live paid vServer write tests")
	}
	if os.Getenv("VNGCLOUD_LIVE_PAID_ENCRYPTED_SERVER") != "1" {
		t.Skip("set VNGCLOUD_LIVE_PAID_ENCRYPTED_SERVER=1 to run the live encrypted server test; " +
			"it orders a real, billed server and volume and needs the owner's approval")
	}
	budgetCap := liveMonitorMaxVND(t)
	const zoneID = "HCM03-1C" // the test account's only enabled zone

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	cfg := liveWriteConfig(ctx, t)
	computeClient := compute.New(cfg)
	volumeClient := volume.New(cfg)
	networkClient := network.New(cfg)
	portalClient := portal.New(cfg)
	billingClient := billing.New(cfg)

	sweptServers := deleteLiveServers(ctx, t, computeClient, volumeClient)
	sweptVolumes := deleteLiveVolumes(ctx, t, volumeClient)
	t.Logf("step 1: deleted %d leftover server(s), %d leftover volume(s)", sweptServers, sweptVolumes)

	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix
	rootTypeID := liveEncryptionTypeID(ctx, t, volumeClient, "aes-xts-plain64_256")
	dataTypeID := liveEncryptionTypeID(ctx, t, volumeClient, "aes-xts-plain64_128")

	vpcID, subnetID := createLiveVPCAndSubnet(ctx, t, networkClient, portalClient, nil)
	group, err := networkClient.CreateSecurityGroup(ctx, &network.CreateSecurityGroupInput{Name: name, Description: "vngcloud live encrypted server test"})
	if err != nil {
		t.Fatalf("step 2 CreateSecurityGroup: %s", safeErr(err))
	}
	groupID := group.SecurityGroup.ID
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := networkClient.DeleteSecurityGroup(cleanupCtx, &network.DeleteSecurityGroupInput{SecurityGroupID: groupID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: DeleteSecurityGroup: %s", safeErr(err))
		}
	})
	rsaKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatalf("step 2 generate rsa key: %v", err)
	}
	sshKey, err := computeClient.ImportSSHKey(ctx, &compute.ImportSSHKeyInput{Name: name, PublicKey: sshRSAPublicKeyLine(&rsaKey.PublicKey, name)})
	if err != nil {
		t.Fatalf("step 2 ImportSSHKey: %s", safeErr(err))
	}
	sshKeyID := sshKey.SSHKey.ID
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := computeClient.DeleteSSHKey(cleanupCtx, &compute.DeleteSSHKeyInput{SSHKeyID: sshKeyID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: DeleteSSHKey: %s", safeErr(err))
		}
	})
	t.Log("step 2: created VPC, subnet, security group, and SSH key")

	flavorZones, err := computeClient.ListFlavorZones(ctx, &compute.ListFlavorZonesInput{ZoneID: zoneID})
	if err != nil {
		t.Fatalf("step 3 ListFlavorZones: %s", safeErr(err))
	}
	var flavorID string
	for _, fz := range flavorZones.Items {
		flavors, err := computeClient.ListFlavors(ctx, &compute.ListFlavorsInput{FlavorZoneID: fz.ID})
		if err != nil {
			t.Fatalf("step 3 ListFlavors: %s", safeErr(err))
		}
		for _, f := range flavors.Items {
			if f.Name == "s2-general-1x2" {
				flavorID = f.FlavorID
			}
		}
	}
	if flavorID == "" {
		t.Fatal("step 3: flavor s2-general-1x2 not found")
	}
	images, err := computeClient.ListOSImages(ctx, &compute.ListOSImagesInput{ZoneID: zoneID})
	if err != nil {
		t.Fatalf("step 3 ListOSImages: %s", safeErr(err))
	}
	var imageID string
	for _, img := range images.Items {
		if strings.Contains(img.ImageVersion, "24.04") {
			imageID = img.ID
			break
		}
	}
	if imageID == "" {
		t.Fatal("step 3: Ubuntu 24.04 image not found")
	}
	volType, err := volumeClient.GetDefaultVolumeType(ctx, &volume.GetDefaultVolumeTypeInput{ZoneID: zoneID})
	if err != nil {
		t.Fatalf("step 3 GetDefaultVolumeType: %s", safeErr(err))
	}

	serverInput := &compute.CreateServerInput{
		Name: name, ZoneID: zoneID, FlavorID: flavorID, ImageID: imageID,
		VPCID: vpcID, SubnetID: subnetID, SecurityGroupIDs: []string{groupID},
		SSHKeyID: sshKeyID, RootDiskSize: 20, RootDiskTypeID: volType.VolumeType.ID,
		RootDiskEncryptionTypeID: rootTypeID,
		DataDiskSize:             20, DataDiskTypeID: volType.VolumeType.ID, DataDiskName: name + "-data",
		DataDiskEncryptionTypeID: dataTypeID,
	}
	volumeInput := &volume.CreateVolumeInput{
		Name: name + "-vol", ZoneID: zoneID, Size: 10, VolumeTypeID: volType.VolumeType.ID,
		EncryptionTypeID: rootTypeID,
	}

	serverQuote, err := computeClient.QuoteCreateServer(ctx, serverInput)
	if err != nil {
		t.Fatalf("step 4 QuoteCreateServer: %s", safeErr(err))
	}
	volumeQuote, err := volumeClient.QuoteCreateVolume(ctx, volumeInput)
	if err != nil {
		t.Fatalf("step 4 QuoteCreateVolume: %s", safeErr(err))
	}
	t.Logf("step 4: server quote %.0f VND, volume quote %.0f VND", serverQuote.OptimumPrice, volumeQuote.OptimumPrice)
	if serverQuote.OptimumPrice > budgetCap || volumeQuote.OptimumPrice > budgetCap {
		t.Fatalf("step 4: a quote exceeds this run's cap %.0f VND; ordering nothing", budgetCap)
	}
	before := liveCash(ctx, t, billingClient)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		t.Logf("cleanup: deleted %d vngcloud-live server(s)", deleteLiveServers(cleanupCtx, t, computeClient, volumeClient))
		t.Logf("cleanup: deleted %d vngcloud-live volume(s)", deleteLiveVolumes(cleanupCtx, t, volumeClient))
		assertNoLiveServersOrVolumesRemain(cleanupCtx, t, computeClient, volumeClient)
	})

	serverInput.MaxPrice = serverQuote.OptimumPrice
	createStart := time.Now()
	server, err := computeClient.CreateServer(ctx, serverInput)
	if err != nil {
		t.Fatalf("step 5 CreateServer: %s", safeErr(err))
	}
	serverID := server.Server.UUID
	t.Logf("step 5: settled after %s at status %s", time.Since(createStart), server.Server.Status)
	if server.Server.Status != "ACTIVE" {
		t.Fatalf("step 5: status = %s, want ACTIVE", server.Server.Status)
	}
	logLiveCashDelta(ctx, t, billingClient, "step 5 after server create", before)

	// ListVolumes rows carry encryptionType; ListVolumesByServer returned no
	// rows for a server with two volumes, so its count is logged beside.
	allVolumes, err := volumeClient.ListVolumes(ctx, nil)
	if err != nil {
		t.Fatalf("step 6 ListVolumes: %s", safeErr(err))
	}
	var onServer, bootEncrypted, dataEncrypted int
	for _, v := range allVolumes.Items {
		if v.ServerID != serverID && !slices.Contains(v.ServerIDList, serverID) {
			continue
		}
		onServer++
		encrypted := v.EncryptionType != nil && *v.EncryptionType != ""
		switch {
		case !encrypted:
		case v.UUID == server.Server.BootVolumeID:
			bootEncrypted++
		default:
			dataEncrypted++
		}
	}
	byServer, err := volumeClient.ListVolumesByServer(ctx, &volume.ListVolumesByServerInput{ServerID: serverID})
	if err != nil {
		t.Fatalf("step 6 ListVolumesByServer: %s", safeErr(err))
	}
	t.Logf("step 6: %d volume(s) on the server, boot volume encrypted: %v, data volumes encrypted: %d; ListVolumesByServer returned %d",
		onServer, bootEncrypted == 1, dataEncrypted, len(byServer.Items))

	volumeInput.MaxPrice = volumeQuote.OptimumPrice
	createdVolume, err := volumeClient.CreateVolume(ctx, volumeInput)
	if err != nil {
		t.Fatalf("step 7 CreateVolume: %s", safeErr(err))
	}
	volumeID := createdVolume.Volume.UUID
	t.Logf("step 7: encrypted volume at status %s", createdVolume.Volume.Status)

	attachStart := time.Now()
	attached, err := volumeClient.AttachVolume(ctx, &volume.AttachVolumeInput{VolumeID: volumeID, ServerID: serverID})
	var apiErr *vngcloud.APIError
	switch {
	case err == nil:
		t.Logf("step 8: attach succeeded after %s, status %s", time.Since(attachStart), attached.Volume.Status)
		if _, err := computeClient.StopServer(ctx, &compute.StopServerInput{ServerID: serverID}); err != nil {
			t.Fatalf("step 8 StopServer: %s", safeErr(err))
		}
		if _, err := volumeClient.DetachVolume(ctx, &volume.DetachVolumeInput{VolumeID: volumeID, ServerID: serverID}); err != nil {
			t.Fatalf("step 8 DetachVolume: %s", safeErr(err))
		}
		t.Log("step 8: detached the encrypted volume")
	case errors.As(err, &apiErr):
		t.Logf("step 8: attach refused: status=%d code=%s, message names the encryption refusal: %v",
			apiErr.StatusCode, apiErr.Code, strings.Contains(strings.ToLower(apiErr.Message), "cannot attach encryption volume"))
	default:
		t.Fatalf("step 8 AttachVolume: %s", safeErr(err))
	}

	if _, err := volumeClient.DeleteVolume(ctx, &volume.DeleteVolumeInput{VolumeID: volumeID}); err != nil {
		t.Fatalf("step 9 DeleteVolume: %s", safeErr(err))
	}
	if _, err := computeClient.DeleteServer(ctx, &compute.DeleteServerInput{ServerID: serverID, DeleteVolumes: true}); err != nil {
		t.Fatalf("step 9 DeleteServer: %s", safeErr(err))
	}
	if _, err := volumeClient.GetVolume(ctx, &volume.GetVolumeInput{VolumeID: volumeID}); !vngcloud.IsNotFound(err) {
		t.Fatalf("step 9: GetVolume after delete = %s, want NotFound", safeErr(err))
	}
	if _, err := computeClient.GetServer(ctx, &compute.GetServerInput{ServerID: serverID}); !vngcloud.IsNotFound(err) {
		t.Fatalf("step 9: GetServer after delete = %s, want NotFound", safeErr(err))
	}
	t.Log("step 9: confirmed the volume and the server are gone")
	assertNoLiveServersOrVolumesRemain(ctx, t, computeClient, volumeClient)
	logLiveRefund(ctx, t, billingClient, "step 9 after deletes", before)
}
