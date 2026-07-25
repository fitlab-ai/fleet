package compat

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var historicalTests = []string{
	"CliSecurityTests.test_status_does_not_reveal_credential",
	"CliSecurityTests.test_url_validation_requires_https",
	"DependencyVersionTests.test_accepts_current_stable_sing_box_version",
	"DependencyVersionTests.test_rejects_old_prerelease_or_unparseable_sing_box_versions",
	"DiagnosticsTests.test_health_command_keeps_order_and_returns_nonzero",
	"DiagnosticsTests.test_health_retries_after_early_core_exit",
	"DiagnosticsTests.test_ping_is_explicitly_tcp_only",
	"GenerationStoreTests.test_active_refresh_lock_is_rejected",
	"GenerationStoreTests.test_bad_pointer_falls_back_to_legacy",
	"GenerationStoreTests.test_composite_writer_lock_releases_legacy_when_writer_is_busy",
	"GenerationStoreTests.test_corrupt_current_generation_falls_back",
	"GenerationStoreTests.test_loads_legacy_manifest_without_trojan_count",
	"GenerationStoreTests.test_publish_and_load",
	"GenerationStoreTests.test_rejects_manifest_with_unknown_nonzero_protocol",
	"KeychainCredentialTests.test_each_subscription_uses_a_distinct_keychain_account",
	"MultiSubscriptionTests.test_add_keeps_multiple_credentials_and_prints_stable_identity",
	"MultiSubscriptionTests.test_aggregation_preserves_order_source_and_duplicate_names",
	"MultiSubscriptionTests.test_explicit_migrate_first_preserves_credentialed_legacy_subscription",
	"MultiSubscriptionTests.test_legacy_cache_without_credential_is_not_hidden_by_first_add",
	"MultiSubscriptionTests.test_migrate_reports_safe_subscription_error_detail",
	"MultiSubscriptionTests.test_refresh_isolates_failure_and_preserves_failed_cache",
	"MultiSubscriptionTests.test_refresh_isolates_per_subscription_disk_failure",
	"MultiSubscriptionTests.test_refresh_publishes_trojan_count_to_subscription_state",
	"MultiSubscriptionTests.test_remove_retains_marked_cache_until_refresh_purges_it",
	"MultiSubscriptionTests.test_remove_rolls_registry_back_when_credential_delete_fails",
	"MultiSubscriptionTests.test_ten_subscriptions_and_five_hundred_nodes_remain_distinct",
	"NodeValidationTests.test_hysteria2_normalizes_connection_fields",
	"NodeValidationTests.test_hysteria2_rejects_unsupported_and_invalid_fields_without_secrets",
	"NodeValidationTests.test_quantity_guard",
	"NodeValidationTests.test_rejects_unknown_protocol_and_duplicate_names",
	"NodeValidationTests.test_supported_protocols_and_counts",
	"NodeValidationTests.test_trojan_accepts_tcp_tls_fields",
	"NodeValidationTests.test_trojan_rejects_invalid_tls_and_transport_fields",
	"OutboundConfigTests.test_hysteria2_outbound_preserves_normalized_connection_semantics",
	"OutboundConfigTests.test_trojan_outbound_maps_tls_fields",
	"OutboundConfigTests.test_trojan_tls_defaults_and_proxy_tun_outbound_match",
	"RefreshPipelineTests.test_download_negotiates_clash_meta_without_changing_request_contract",
	"RefreshPipelineTests.test_force_does_not_bypass_protocol_validation",
	"RefreshPipelineTests.test_format_failure_is_safe_and_preserves_current_generation",
	"RefreshPipelineTests.test_process_publishes_mixed_nodes_including_trojan",
	"RefreshPipelineTests.test_process_validates_before_publishing",
	"RefreshPipelineTests.test_sing_box_failure_identifies_node",
	"RefreshPipelineTests.test_strict_parser_distinguishes_format_from_clash_structure",
	"RefreshPipelineTests.test_strict_parser_rejects_invalid_yaml",
	"SecureFileTests.test_secure_write_closes_fd_when_fdopen_fails",
	"SubscriptionRegistryTests.test_names_are_case_insensitive_unique_and_removed_names_stay_reserved",
	"SubscriptionRegistryTests.test_registry_rejects_secret_fields_and_does_not_overwrite_corruption",
	"SubscriptionRegistryTests.test_removed_records_are_purged_and_names_can_be_reused",
}

var equivalentRE = regexp.MustCompile(`^([^.\s]+(?:/[^.\s]+)*)\.(Test\w+)$`)

func TestCompatibilityMatrixTracksEveryHistoricalBehavior(t *testing.T) {
	matrix, err := os.Open("../compatibility-matrix.md")
	if err != nil {
		t.Fatal(err)
	}
	defer matrix.Close()

	want := append([]string(nil), historicalTests...)
	sort.Strings(want)
	seen := map[string]bool{}
	var got []string
	scanner := bufio.NewScanner(matrix)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) != 8 {
			t.Fatalf("malformed matrix row: %s", line)
		}
		id := strings.Trim(strings.TrimSpace(fields[1]), "`")
		equivalent := strings.Trim(strings.TrimSpace(fields[2]), "`")
		status := strings.Trim(strings.TrimSpace(fields[5]), "`")
		evidence := strings.Trim(strings.TrimSpace(fields[6]), "`")
		if seen[id] {
			t.Errorf("duplicate historical ID %s", id)
		}
		seen[id] = true
		got = append(got, id)
		if status != "verified" {
			t.Errorf("%s has non-terminal status %q", id, status)
		}
		match := equivalentRE.FindStringSubmatch(equivalent)
		if match == nil {
			t.Errorf("%s has invalid Go equivalent %q", id, equivalent)
			continue
		}
		if evidence != "go test ./"+match[1]+" -run '^"+match[2]+"$'" {
			t.Errorf("%s has imprecise evidence %q", id, evidence)
		}
		assertTestExists(t, match[1], match[2])
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("historical behavior set mismatch\n got: %q\nwant: %q", got, want)
	}
}

func assertTestExists(t *testing.T, packagePath, testName string) {
	t.Helper()
	dir := filepath.Join("../..", filepath.FromSlash(packagePath))
	packages, err := parser.ParseDir(token.NewFileSet(), dir, func(info os.FileInfo) bool {
		return strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Errorf("%s.%s: parse %s: %v", packagePath, testName, dir, err)
		return
	}
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			for _, declaration := range file.Decls {
				if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == testName {
					return
				}
			}
		}
	}
	t.Errorf("%s.%s: test symbol does not exist", packagePath, testName)
}
