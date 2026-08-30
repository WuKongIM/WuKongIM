import 'dart:async';

import 'package:wukongimfluttersdk/common/options.dart';
import 'package:wukongimfluttersdk/entity/channel.dart';
import 'package:wukongimfluttersdk/entity/conversation.dart';
import 'package:wukongimfluttersdk/entity/msg.dart';
import 'package:wukongimfluttersdk/manager/connect_manager.dart';
import 'package:wukongimfluttersdk/model/wk_text_content.dart';
import 'package:wukongimfluttersdk/type/const.dart';
import 'package:wukongimfluttersdk/wkim.dart';

class WKBootstrap {
  const WKBootstrap({
    required this.uid,
    required this.token,
    required this.address,
    required this.deviceFlag,
  });

  final String uid;
  final String token;
  final String address;
  final int deviceFlag;
}

typedef WKConversationSource =
    Future<WKSyncConversation> Function(
      String uid,
      String lastMsgSeqs,
      int msgCount,
      int version,
    );

abstract class WKSessionObserver {
  void onConnectionState(int status, int? reason, ConnectionInfo? info);
  void onReady();
  void onTimeout();
  void onTerminalError(Object error);
  void onLocalInsert(WKMsg message);
  void onSendSucceeded(WKMsg message);
  void onSendRejected(WKMsg message);
  void onNewMessages(List<WKMsg> messages);
}

class WKProviders {
  WKProviders(this._conversationSource);

  final WKConversationSource _conversationSource;
  WKSession? _active;
  int _activationEpoch = 0;
  int _connectionEpoch = 0;
  int _deliveredConnectionEpoch = -1;
  String? _processUid;
  bool _installed = false;
  bool _restartBlocked = false;

  void install() {
    if (_installed) return;
    _installed = true;

    WKIM.shared.conversationManager.addOnSyncConversationListener((
      lastMsgSeqs,
      msgCount,
      version,
      complete,
    ) {
      final session = _active;
      final activationEpoch = _activationEpoch;
      final connectionEpoch = _connectionEpoch;
      if (session == null || connectionEpoch == 0 || _restartBlocked) return;

      _loadConversation(
        session,
        activationEpoch,
        connectionEpoch,
        lastMsgSeqs,
        msgCount,
        version,
        complete,
      );
    });

    // This SDK callback has no key and no remove API. Install it once and
    // dispatch through the application-scoped provider.
    WKIM.shared.messageManager.addOnMsgInsertedListener((message) {
      _active?._onInserted(message);
    });
  }

  Future<void> _loadConversation(
    WKSession session,
    int activationEpoch,
    int connectionEpoch,
    String lastMsgSeqs,
    int msgCount,
    int version,
    Function(WKSyncConversation) complete,
  ) async {
    try {
      final result = await _conversationSource(
        session.bootstrap.uid,
        lastMsgSeqs,
        msgCount,
        version,
      );
      if (_active == session &&
          _activationEpoch == activationEpoch &&
          _connectionEpoch == connectionEpoch &&
          !_restartBlocked) {
        _deliveredConnectionEpoch = connectionEpoch;
        complete(result);
      }
    } catch (error) {
      if (_active == session &&
          _activationEpoch == activationEpoch &&
          _connectionEpoch == connectionEpoch) {
        session._scheduleTerminal(error);
      }
    }
  }

  int activate(WKSession session, WKBootstrap bootstrap) {
    if (!_installed) {
      throw StateError('install application-scoped providers before start');
    }
    if (_restartBlocked) {
      throw StateError('restart the process before another connection attempt');
    }
    if (_active != null) {
      throw StateError('another WKIM session is active in this process');
    }
    if (_processUid != null && _processUid != bootstrap.uid) {
      throw StateError('use process isolation to switch uid');
    }
    _processUid = bootstrap.uid;
    _active = session;
    _connectionEpoch = 0;
    _deliveredConnectionEpoch = -1;
    return ++_activationEpoch;
  }

  int beginConnection(WKSession session, int activationEpoch) {
    if (_active != session || _activationEpoch != activationEpoch) {
      throw StateError('stale connection generation');
    }
    _deliveredConnectionEpoch = -1;
    return ++_connectionEpoch;
  }

  bool didDeliver(WKSession session, int activationEpoch, int connectionEpoch) {
    return _active == session &&
        _activationEpoch == activationEpoch &&
        _connectionEpoch == connectionEpoch &&
        _deliveredConnectionEpoch == connectionEpoch;
  }

  void requireProcessRestart() {
    _restartBlocked = true;
  }

  void deactivate(WKSession session, int activationEpoch) {
    if (_active == session && _activationEpoch == activationEpoch) {
      _active = null;
      _connectionEpoch = 0;
      _deliveredConnectionEpoch = -1;
      ++_activationEpoch;
    }
  }

  static WKConversationSource freshAccountsOnly() {
    return (uid, lastMsgSeqs, msgCount, version) async {
      final result = WKSyncConversation();
      result.uid = uid;
      result.conversations = <WKSyncConvMsg>[];
      return result;
    };
  }
}

class WKSession {
  WKSession(
    this._providers,
    this._observer, {
    this.acceptanceTimeout = const Duration(seconds: 15),
    this.sendTimeout = const Duration(seconds: 15),
  });

  final WKProviders _providers;
  final WKSessionObserver _observer;
  final Duration acceptanceTimeout;
  final Duration sendTimeout;
  final String _listenerKey =
      'wk-flutter-session-${DateTime.now().microsecondsSinceEpoch}';

  WKBootstrap? _bootstrap;
  int _activationEpoch = 0;
  int _connectionEpoch = 0;
  int _connectingCount = 0;
  bool _sawSync = false;
  bool _ready = false;
  bool _terminalScheduled = false;
  Timer? _acceptanceTimer;
  Timer? _sendTimer;
  bool _awaitingInsert = false;
  bool _insertReported = false;
  WKMsg? _pendingMessage;
  WKMsg? _earlyTerminalRefresh;

  WKBootstrap get bootstrap {
    final value = _bootstrap;
    if (value == null) throw StateError('session is not active');
    return value;
  }

  Future<void> start(WKBootstrap next) async {
    if (_bootstrap != null) throw StateError('session is already active');
    if (next.uid.trim().isEmpty || next.token.trim().isEmpty) {
      throw ArgumentError('uid and token are required');
    }
    if (next.address.split(':').length != 2) {
      throw ArgumentError('address must be a DNS-or-IPv4 host:port pair');
    }
    if (next.deviceFlag != 0 && next.deviceFlag != 2) {
      throw ArgumentError('native Flutter targets must use 0=APP or 2=PC');
    }

    _activationEpoch = _providers.activate(this, next);
    _bootstrap = next;

    final options = Options.newDefault(next.uid, next.token, addr: next.address)
      ..debug = false
      ..deviceFlag = next.deviceFlag;

    try {
      final initialized = await WKIM.shared.setup(options);
      if (!initialized) throw StateError('WKIM setup failed');
      _registerKeyedListeners();
      _acceptanceTimer = Timer(acceptanceTimeout, () {
        _scheduleTerminal(
          TimeoutException('WKIM acceptance timed out', acceptanceTimeout),
          timeout: true,
        );
      });
      WKIM.shared.connectionManager.connect();
    } catch (error) {
      _scheduleTerminal(error);
      rethrow;
    }
  }

  void _registerKeyedListeners() {
    WKIM.shared.connectionManager.addOnConnectionStatus(_listenerKey, (
      status,
      reason,
      info,
    ) {
      if (_bootstrap == null || _terminalScheduled) return;
      scheduleMicrotask(() {
        if (_bootstrap != null) {
          _observer.onConnectionState(status, reason, info);
        }
      });

      if (status == WKConnectStatus.connecting) {
        _connectingCount++;
        if (_connectingCount > 1) {
          _scheduleTerminal(
            StateError('automatic reconnect has no public generation fence'),
          );
          return;
        }
        _ready = false;
        _sawSync = false;
        return;
      }
      if (status == WKConnectStatus.success) {
        _ready = false;
        _sawSync = false;
        _connectionEpoch = _providers.beginConnection(this, _activationEpoch);
        return;
      }
      if (status == WKConnectStatus.syncMsg) {
        _ready = false;
        _sawSync = true;
        return;
      }
      if (status == WKConnectStatus.syncCompleted &&
          _sawSync &&
          _providers.didDeliver(this, _activationEpoch, _connectionEpoch)) {
        _ready = true;
        _acceptanceTimer?.cancel();
        _acceptanceTimer = null;
        scheduleMicrotask(() {
          if (_bootstrap != null && _ready) _observer.onReady();
        });
        return;
      }
      if (status == WKConnectStatus.kicked) {
        _scheduleTerminal(
          StateError('the server kicked this session'),
          sdkAlreadyLoggedOut: true,
        );
        return;
      }
      if (status == WKConnectStatus.noNetwork) {
        _scheduleTerminal(StateError('network became unavailable'));
        return;
      }
      // connect() emits a synthetic fail with no reason before connecting.
      // A non-null reason is the CONNACK rejection path.
      if (status == WKConnectStatus.fail && reason != null) {
        _scheduleTerminal(StateError('CONNACK rejected: $reason'));
      }
    });

    WKIM.shared.messageManager.addOnRefreshMsgListener(
      _listenerKey,
      _onRefresh,
    );
    WKIM.shared.messageManager.addOnNewMsgListener(_listenerKey, (messages) {
      final snapshot = List<WKMsg>.unmodifiable(messages);
      scheduleMicrotask(() {
        if (_bootstrap != null) _observer.onNewMessages(snapshot);
      });
    });
  }

  Future<WKMsg> sendText(String text, String peerUid) async {
    if (!_ready) throw StateError('conversation sync is incomplete');
    if (text.trim().isEmpty || peerUid.trim().isEmpty) {
      throw ArgumentError('text and peerUid are required');
    }
    if (_awaitingInsert || _pendingMessage != null) {
      throw StateError('this acceptance helper allows one in-flight message');
    }

    _awaitingInsert = true;
    _insertReported = false;
    _earlyTerminalRefresh = null;
    try {
      await WKIM.shared.messageManager.sendWithOption(
        WKTextContent(text),
        WKChannel(peerUid, WKChannelType.personal),
        WKSendOptions(),
      );
    } catch (error) {
      _awaitingInsert = false;
      _scheduleTerminal(error);
      rethrow;
    }
    _awaitingInsert = false;

    final inserted = _pendingMessage;
    if (inserted == null || inserted.clientSeq <= 0) {
      final error = StateError('the durable local insert did not complete');
      _scheduleTerminal(error);
      throw error;
    }

    _insertReported = true;
    final early = _earlyTerminalRefresh;
    if (early == null) {
      _sendTimer = Timer(sendTimeout, () {
        _scheduleTerminal(TimeoutException('SENDACK timed out', sendTimeout));
      });
    }
    scheduleMicrotask(() {
      _observer.onLocalInsert(inserted);
    });
    if (early != null) _completeSend(early);
    return inserted;
  }

  void _onInserted(WKMsg message) {
    if (!_awaitingInsert || _pendingMessage != null || _bootstrap == null) {
      return;
    }
    _pendingMessage = message;
  }

  void _onRefresh(WKMsg message) {
    final pending = _pendingMessage;
    if (pending == null ||
        pending.clientMsgNO != message.clientMsgNO ||
        pending.clientSeq != message.clientSeq ||
        message.status == WKSendMsgResult.sendLoading) {
      return;
    }
    if (!_insertReported) {
      _earlyTerminalRefresh = message;
      return;
    }
    _completeSend(message);
  }

  void _completeSend(WKMsg message) {
    final succeeded = message.status == WKSendMsgResult.sendSuccess;
    _sendTimer?.cancel();
    _sendTimer = null;
    _pendingMessage = null;
    _earlyTerminalRefresh = null;
    _insertReported = false;
    scheduleMicrotask(() {
      if (_bootstrap != null) {
        if (succeeded) {
          _observer.onSendSucceeded(message);
        } else {
          _observer.onSendRejected(message);
        }
      }
    });
  }

  void _scheduleTerminal(
    Object error, {
    bool timeout = false,
    bool sdkAlreadyLoggedOut = false,
  }) {
    if (_terminalScheduled || _bootstrap == null) return;
    _terminalScheduled = true;
    _ready = false;
    _providers.requireProcessRestart();
    scheduleMicrotask(() {
      _finish(logout: true, disconnect: !sdkAlreadyLoggedOut);
      if (timeout) {
        _observer.onTimeout();
      } else {
        _observer.onTerminalError(error);
      }
    });
  }

  void close() {
    if (_awaitingInsert || _pendingMessage != null) {
      throw StateError('wait for a terminal SENDACK before teardown');
    }
    _providers.requireProcessRestart();
    _finish(logout: true, disconnect: true);
  }

  void _finish({required bool logout, required bool disconnect}) {
    if (_bootstrap == null) return;
    _acceptanceTimer?.cancel();
    _sendTimer?.cancel();
    _acceptanceTimer = null;
    _sendTimer = null;
    _ready = false;

    WKIM.shared.connectionManager.removeOnConnectionStatus(_listenerKey);
    WKIM.shared.messageManager.removeOnRefreshMsgListener(_listenerKey);
    WKIM.shared.messageManager.removeNewMsgListener(_listenerKey);

    _providers.deactivate(this, _activationEpoch);
    _bootstrap = null;
    if (disconnect) WKIM.shared.connectionManager.disconnect(logout);
  }
}
