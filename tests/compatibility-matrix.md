# Python → Go compatibility matrix

| Python Test | Go Equivalent | Coverage Type | Fixture/Environment | Status | Evidence |
|---|---|---|---|---|---|
| `NodeValidationTests.test_supported_protocols_and_counts` | `internal/subscription.TestValidateNodesProtocolsCountsAndDuplicates` | `unit` | inline nodes | `verified` | `go test ./internal/subscription` |
| `NodeValidationTests.test_rejects_unknown_protocol_and_duplicate_names` | `internal/subscription.TestValidateNodesProtocolsCountsAndDuplicates` | `unit` | inline nodes | `verified` | `go test ./internal/subscription` |
| `NodeValidationTests.test_trojan_rejects_invalid_tls_and_transport_fields` | `internal/subscription.TestValidateNodesProtocolsCountsAndDuplicates` | `unit` | inline nodes | `implemented` | `internal/subscription/validate.go` |
| `NodeValidationTests.test_trojan_accepts_tcp_tls_fields` | `internal/backend.TestProxyAndTUNShareTrojanOutbound` | `unit` | inline node | `verified` | `go test ./internal/backend` |
| `NodeValidationTests.test_quantity_guard` | `internal/subscription.TestValidateNodesProtocolsCountsAndDuplicates` | `unit` | inline counts | `implemented` | `internal/subscription.EnforceNodeCount` |
| `NodeValidationTests.test_hysteria2_normalizes_connection_fields` | `internal/backend.TestHysteria2MapsConnectionOptions` | `unit` | inline node | `verified` | `go test ./internal/backend` |
| `NodeValidationTests.test_hysteria2_rejects_unsupported_and_invalid_fields_without_secrets` | `internal/subscription.TestValidateNodesProtocolsCountsAndDuplicates` | `unit` | inline node | `implemented` | `internal/subscription/validate.go` |
| `GenerationStoreTests.test_publish_and_load` | `internal/store.TestGenerationFallsBackWhenCurrentIsDamaged` | `unit` | temp tree | `verified` | `go test ./internal/store` |
| `GenerationStoreTests.test_loads_legacy_manifest_without_trojan_count` | `internal/store.TestGenerationFallsBackWhenCurrentIsDamaged` | `unit` | legacy manifest | `implemented` | `internal/store/generation.go` |
| `GenerationStoreTests.test_rejects_manifest_with_unknown_nonzero_protocol` | `N/A` | `unit` | manifest fixture | `planned` | - |
| `GenerationStoreTests.test_corrupt_current_generation_falls_back` | `internal/store.TestGenerationFallsBackWhenCurrentIsDamaged` | `unit` | temp tree | `verified` | `go test ./internal/store` |
| `GenerationStoreTests.test_bad_pointer_falls_back_to_legacy` | `internal/store.TestGenerationFallsBackWhenCurrentIsDamaged` | `unit` | legacy nodes | `implemented` | `internal/store/generation.go` |
| `GenerationStoreTests.test_active_refresh_lock_is_rejected` | `N/A` | `unit` | live PID | `implemented` | `internal/store/lock.go` |
| `GenerationStoreTests.test_composite_writer_lock_releases_legacy_when_writer_is_busy` | `N/A` | `unit` | lock fixture | `implemented` | `internal/store/lock.go` |
| `SubscriptionRegistryTests.test_names_are_case_insensitive_unique_and_removed_names_stay_reserved` | `internal/app.TestSubscriptionAddStoresNoSecretOnDisk` | `unit` | temp registry | `implemented` | `internal/store/registry.go` |
| `SubscriptionRegistryTests.test_registry_rejects_secret_fields_and_does_not_overwrite_corruption` | `internal/app.TestSubscriptionAddStoresNoSecretOnDisk` | `unit` | corrupt registry | `implemented` | `internal/store/registry.go` |
| `SubscriptionRegistryTests.test_removed_records_are_purged_and_names_can_be_reused` | `N/A` | `unit` | temp registry | `implemented` | `internal/store.RegistryStore.PurgeRemoved` |
| `MultiSubscriptionTests.test_add_keeps_multiple_credentials_and_prints_stable_identity` | `internal/app.TestSubscriptionAddStoresNoSecretOnDisk` | `blackbox` | memory Keychain | `verified` | `go test ./internal/app` |
| `MultiSubscriptionTests.test_legacy_cache_without_credential_is_not_hidden_by_first_add` | `N/A` | `blackbox` | legacy temp tree | `implemented` | `internal/app.ensureRegistryMigrated` |
| `MultiSubscriptionTests.test_explicit_migrate_first_preserves_credentialed_legacy_subscription` | `N/A` | `blackbox` | 44-node fixture | `implemented` | `internal/app.SubscriptionMigrate` |
| `MultiSubscriptionTests.test_migrate_reports_safe_subscription_error_detail` | `N/A` | `unit` | failing parser | `implemented` | `internal/app.SubscriptionMigrate` |
| `MultiSubscriptionTests.test_aggregation_preserves_order_source_and_duplicate_names` | `internal/model.TestResolveNodeSupportsSourceAndRejectsAmbiguousNames` | `unit` | duplicate nodes | `verified` | `go test ./internal/model` |
| `MultiSubscriptionTests.test_remove_retains_marked_cache_until_refresh_purges_it` | `N/A` | `blackbox` | temp tree | `implemented` | `internal/app.SubscriptionRemove` |
| `MultiSubscriptionTests.test_refresh_isolates_failure_and_preserves_failed_cache` | `N/A` | `blackbox` | fake HTTPS/sing-box | `implemented` | `internal/app.Refresh` |
| `MultiSubscriptionTests.test_refresh_isolates_per_subscription_disk_failure` | `N/A` | `unit` | fault injection | `planned` | - |
| `MultiSubscriptionTests.test_refresh_publishes_trojan_count_to_subscription_state` | `N/A` | `blackbox` | mixed fixture | `implemented` | `internal/app.Refresh` |
| `MultiSubscriptionTests.test_remove_rolls_registry_back_when_credential_delete_fails` | `N/A` | `unit` | failing credential | `implemented` | `internal/app.SubscriptionRemove` |
| `MultiSubscriptionTests.test_ten_subscriptions_and_five_hundred_nodes_remain_distinct` | `N/A` | `unit` | generated nodes | `implemented` | `internal/app.LoadNodes` |
| `KeychainCredentialTests.test_each_subscription_uses_a_distinct_keychain_account` | `internal/credential.TestAccountUsesStableSubscriptionIdentity` | `unit` | fake Runner | `verified` | `go test ./internal/credential` |
| `OutboundConfigTests.test_hysteria2_outbound_preserves_normalized_connection_semantics` | `internal/backend.TestHysteria2MapsConnectionOptions` | `unit` | inline node | `verified` | `go test ./internal/backend` |
| `OutboundConfigTests.test_trojan_outbound_maps_tls_fields` | `internal/backend.TestProxyAndTUNShareTrojanOutbound` | `unit` | inline node | `verified` | `go test ./internal/backend` |
| `OutboundConfigTests.test_trojan_tls_defaults_and_proxy_tun_outbound_match` | `internal/backend.TestProxyAndTUNShareTrojanOutbound` | `unit` | inline node | `verified` | `go test ./internal/backend` |
| `RefreshPipelineTests.test_download_negotiates_clash_meta_without_changing_request_contract` | `N/A` | `unit` | httptest TLS | `implemented` | `internal/subscription.Downloader` |
| `RefreshPipelineTests.test_strict_parser_distinguishes_format_from_clash_structure` | `internal/subscription.TestParseClashYAMLAndRejectTopLevelList` | `unit` | inline YAML | `verified` | `go test ./internal/subscription` |
| `RefreshPipelineTests.test_format_failure_is_safe_and_preserves_current_generation` | `N/A` | `blackbox` | invalid response | `implemented` | `internal/app.Refresh` |
| `RefreshPipelineTests.test_process_publishes_mixed_nodes_including_trojan` | `N/A` | `blackbox` | mixed fixture | `implemented` | `internal/app.Refresh` |
| `RefreshPipelineTests.test_process_validates_before_publishing` | `N/A` | `unit` | fake sing-box | `implemented` | `internal/app.Refresh` |
| `RefreshPipelineTests.test_force_does_not_bypass_protocol_validation` | `N/A` | `blackbox` | invalid protocol | `implemented` | `internal/app.Refresh` |
| `RefreshPipelineTests.test_strict_parser_rejects_invalid_yaml` | `internal/subscription.TestParseClashYAMLAndRejectTopLevelList` | `unit` | inline YAML | `verified` | `go test ./internal/subscription` |
| `RefreshPipelineTests.test_sing_box_failure_identifies_node` | `N/A` | `unit` | fake Runner | `implemented` | `internal/backend.SingBox.ValidateNodes` |
| `DependencyVersionTests.test_accepts_current_stable_sing_box_version` | `N/A` | `unit` | fake version output | `implemented` | `internal/backend.SingBox.CheckVersion` |
| `DependencyVersionTests.test_rejects_old_prerelease_or_unparseable_sing_box_versions` | `N/A` | `unit` | fake version output | `implemented` | `internal/backend.SingBox.CheckVersion` |
| `DiagnosticsTests.test_ping_is_explicitly_tcp_only` | `N/A` | `blackbox` | local TCP listener | `implemented` | `internal/app.Ping` |
| `DiagnosticsTests.test_health_command_keeps_order_and_returns_nonzero` | `N/A` | `blackbox` | fake sing-box/curl | `implemented` | `internal/app.Health` |
| `DiagnosticsTests.test_health_retries_after_early_core_exit` | `N/A` | `blackbox` | fake sing-box | `planned` | - |
| `SecureFileTests.test_secure_write_closes_fd_when_fdopen_fails` | `internal/store.TestAtomicJSONUsesSecureMode` | `unit` | temp tree | `verified` | `go test ./internal/store` |
| `CliSecurityTests.test_url_validation_requires_https` | `internal/subscription.TestValidateURLRequiresHTTPSAndNoUserInfo` | `unit` | inline URLs | `verified` | `go test ./internal/subscription` |
| `CliSecurityTests.test_status_does_not_reveal_credential` | `internal/app.TestSubscriptionAddStoresNoSecretOnDisk` | `blackbox` | memory Keychain | `verified` | `go test ./internal/app` |
