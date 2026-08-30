import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/daemon_api_service.dart';
import '../services/host_manager.dart';

/// The bridge between the `provider` tree and the Riverpod one.
///
/// `HostManager` owns the per-host `DaemonAPIService` instances and stays a
/// `ChangeNotifier`, but a Riverpod provider body has no `BuildContext` to read
/// it from. So the single instance built in `main` is handed to both trees, and
/// this is overridden at the `ProviderScope` that wraps the app.
final hostManagerProvider = Provider<HostManager>(
  (ref) => throw UnimplementedError('override hostManagerProvider in ProviderScope'),
);

final _serviceProvider = Provider.family<DaemonAPIService?, String>(
  (ref, hostId) => ref.watch(hostManagerProvider).serviceFor(hostId),
);

/// Every working directory the host knows, keyed by host.
///
/// Keyed rather than global because two daemons hold different directories and
/// must not answer for each other.
final directoriesProvider = FutureProvider.autoDispose
    .family<List<DirectoryInfo>, String>((ref, hostId) async {
  final service = ref.watch(_serviceProvider(hostId));
  if (service == null) return const [];
  return service.fetchDirectories();
});
