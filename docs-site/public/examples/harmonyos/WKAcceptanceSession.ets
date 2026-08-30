import { Context } from '@kit.AbilityKit'
import { WKIM } from '@wukong/wkim'
import { WKLogger } from '@wukong/wkim/src/main/ets/common/WKLogger'
import {
  ConnectionInfo,
  WKChannel,
  WKChannelType,
  WKConnectStatus,
  WKMsg,
  WKSendMsgResult,
  WKSendOptions,
  WKSyncConversation
} from '@wukong/wkim/src/main/ets/entity/Bean'
import { WKTextContent } from '@wukong/wkim/src/main/ets/model/WKTextContent'

let processActivationClaimed = false

export interface WKBootstrap {
  uid: string
  token: string
  address: string
}

export type ConversationSource = (
  lastMsgSeqs: string,
  msgCount: number,
  version: number
) => Promise<WKSyncConversation>

export interface WKAcceptanceObserver {
  onConnectionStatus(status: number, reasonCode?: number, nodeId?: number): void
  onReady(): void
  onLocalInsert(msg: WKMsg): void
  onSendSucceeded(clientSeq: number, messageId: string, messageSeq: number): void
  onSendRejected(clientSeq: number, reasonCode: number): void
  onLateSendTerminal(
    clientSeq: number,
    messageId: string,
    messageSeq: number,
    reasonCode: number
  ): void
  onNewMessages(msgs: WKMsg[]): void
  requireProcessRestart(reason: string): void
}

/**
 * A one-UID, one-connection, one-message acceptance skeleton for @wukong/wkim 1.1.7.
 * The file is source-aligned but has no site-owned DevEco compile or runtime receipt.
 */
export class WKAcceptanceSession {
  private readonly observer: WKAcceptanceObserver
  private readonly conversationSource: ConversationSource
  private started = false
  private listenersRegistered = false
  private terminated = false
  private ready = false
  private syncDelivered = false
  private expectingInsert = false
  private sentOnce = false
  private sendMayBeUnresolved = false
  private successfulAckSeen = false
  private onlineMessageSeen = false
  private connectingCount = 0
  private successSeen = false
  private syncingSeen = false
  private inFlightClientSeq = 0
  private insertedMessage?: WKMsg
  private readyTimerId = 0
  private sendTimerId = 0

  constructor(observer: WKAcceptanceObserver, conversationSource: ConversationSource) {
    this.observer = observer
    this.conversationSource = conversationSource
  }

  private readonly connectStatusListener = (
    status: number,
    reasonCode?: number,
    connInfo?: ConnectionInfo
  ): void => {
    if (this.terminated) {
      return
    }
    try {
      this.observer.onConnectionStatus(status, reasonCode, connInfo?.nodeId)
    } catch (error) {
      this.terminate(
        `Connection-status observer threw at status ${status}, reason ${reasonCode}: ${error}`
      )
      return
    }

    if (status === WKConnectStatus.connecting) {
      this.connectingCount++
      if (this.connectingCount > 1) {
        this.terminate('A second connection generation started')
      }
      return
    }
    if (status === WKConnectStatus.success) {
      if (this.connectingCount !== 1) {
        this.terminate('success arrived outside the first connection generation')
        return
      }
      this.successSeen = true
      return
    }
    if (status === WKConnectStatus.syncing) {
      if (!this.successSeen) {
        this.terminate('syncing arrived before CONNACK success')
        return
      }
      this.syncingSeen = true
      return
    }
    if (status === WKConnectStatus.syncCompleted) {
      if (!this.successSeen || !this.syncingSeen || !this.syncDelivered) {
        this.terminate('syncCompleted arrived without the complete application gate')
        return
      }
      this.ready = true
      this.clearReadyTimer()
      this.observer.onReady()
      return
    }
    if (status === WKConnectStatus.fail ||
      status === WKConnectStatus.kicked ||
      status === WKConnectStatus.noNetwork) {
      this.terminate(`Terminal connection status ${status}, reason ${reasonCode}`)
    }
  }

  private readonly insertedListener = (msg: WKMsg): void => {
    if (this.terminated || !this.expectingInsert || this.inFlightClientSeq !== 0) {
      return
    }
    if (msg.clientSeq <= 0) {
      return
    }
    this.inFlightClientSeq = msg.clientSeq
    this.insertedMessage = msg
    this.observer.onLocalInsert(msg)
  }

  private readonly sendStatusListener = (
    clientMsgSeq: number,
    messageId: string,
    messageSeq: number,
    reasonCode: number
  ): void => {
    const matchesKnownClientSeq = this.inFlightClientSeq > 0 &&
      clientMsgSeq === this.inFlightClientSeq
    const matchesUncorrelatedSend = this.terminated && this.sendMayBeUnresolved &&
      this.inFlightClientSeq === 0 && clientMsgSeq === 0
    if (!matchesKnownClientSeq && !matchesUncorrelatedSend) {
      return
    }
    this.clearSendTimer()
    this.inFlightClientSeq = 0
    this.sendMayBeUnresolved = false
    if (this.terminated) {
      this.observer.onLateSendTerminal(clientMsgSeq, messageId, messageSeq, reasonCode)
      return
    }
    if (reasonCode === WKSendMsgResult.success) {
      this.successfulAckSeen = true
      this.observer.onSendSucceeded(clientMsgSeq, messageId, messageSeq)
    } else {
      try {
        this.observer.onSendRejected(clientMsgSeq, reasonCode)
      } finally {
        this.terminate(`SENDACK rejected with reason ${reasonCode}`)
      }
    }
  }

  private readonly newMessagesListener = (msgs: WKMsg[]): void => {
    if (this.terminated) {
      return
    }
    if (msgs.length > 0) {
      this.onlineMessageSeen = true
    }
    this.observer.onNewMessages(msgs)
  }

  async start(bootstrap: WKBootstrap, context: Context): Promise<void> {
    if (this.started) {
      throw new Error('WKAcceptanceSession is one-shot')
    }
    this.assertAddress(bootstrap.address)
    if (processActivationClaimed) {
      throw new Error('WKIM.shared is already claimed; restart the process before another session')
    }
    processActivationClaimed = true
    this.started = true

    // WKConfig.debug is set for intent, but 1.1.7 does not read it. WKLogger is the
    // effective SDK switch; two direct hilog.info send traces still bypass it.
    WKLogger.setShowLog(false)
    await WKIM.shared.init(bootstrap.uid, bootstrap.token, bootstrap.address, context)
    WKIM.shared.config.debug = false
    WKIM.shared.config.deviceFlagApp = 0

    WKIM.shared.config.provider.syncConversationCallback = async (
      lastMsgSeqs: string,
      msgCount: number,
      version: number
    ): Promise<WKSyncConversation> => {
      try {
        const result = await this.conversationSource(lastMsgSeqs, msgCount, version)
        this.syncDelivered = true
        return result
      } catch (error) {
        this.terminate(`Conversation synchronization failed: ${error}`)
        throw error
      }
    }

    WKIM.shared.connectionManager().addConnectStatusListener(this.connectStatusListener)
    WKIM.shared.messageManager().addInsertedListener(this.insertedListener)
    WKIM.shared.messageManager().addSendStatusListener(this.sendStatusListener)
    WKIM.shared.messageManager().addNewMsgListener(this.newMessagesListener)
    this.listenersRegistered = true

    this.readyTimerId = setTimeout(() => {
      this.terminate('Readiness timed out after 15 seconds')
    }, 15000)
    WKIM.shared.connectionManager().connection()
  }

  sendText(content: string, recipientUID: string): WKMsg {
    if (!this.ready || this.terminated) {
      throw new Error('Wait for application-gated syncCompleted before sending')
    }
    if (this.sentOnce || this.inFlightClientSeq !== 0) {
      throw new Error('The acceptance session allows exactly one in-flight message')
    }
    if (content.length === 0 || recipientUID.length === 0) {
      throw new Error('Message content and recipient UID are required')
    }

    this.sentOnce = true
    this.expectingInsert = true
    this.insertedMessage = undefined
    const text = new WKTextContent(content)
    const channel = new WKChannel(recipientUID, WKChannelType.personal)
    this.sendMayBeUnresolved = true
    try {
      WKIM.shared.messageManager().sendWithOption(text, channel, new WKSendOptions())
    } catch (error) {
      this.expectingInsert = false
      this.terminate(`sendWithOption threw: ${error}`)
      throw error
    }
    this.expectingInsert = false

    if (this.inFlightClientSeq <= 0) {
      this.terminate('The durable text did not produce a local insert')
      throw new Error('Missing local insert')
    }
    const inserted = this.insertedMessage
    if (inserted === undefined) {
      this.terminate('The inserted message could not be read back')
      throw new Error('Missing inserted message')
    }

    this.sendTimerId = setTimeout(() => {
      this.terminate('SENDACK timed out after 15 seconds')
    }, 15000)
    return inserted
  }

  closeAfterTerminalAck(): void {
    if (this.terminated) {
      throw new Error('The acceptance session is already terminal')
    }
    if (!this.sentOnce || !this.successfulAckSeen || this.sendMayBeUnresolved ||
      this.inFlightClientSeq !== 0) {
      throw new Error('Close the sender only after its successful terminal SENDACK')
    }
    this.terminate('Sender acceptance complete')
  }

  closeReceiverAfterVerifiedOnlineReceipt(): void {
    if (this.terminated) {
      throw new Error('The acceptance session is already terminal')
    }
    if (this.sentOnce) {
      throw new Error('Use closeAfterTerminalAck for a sending session')
    }
    if (!this.onlineMessageSeen) {
      throw new Error('Verify the expected online message before closing the receiver')
    }
    this.terminate('Receiver acceptance complete')
  }

  private assertAddress(address: string): void {
    const parts = address.split(':')
    if (parts.length !== 2 || parts[0].length === 0 || parts[1].length === 0) {
      throw new Error('address must be one DNS/IPv4 host:port value')
    }
    const port = Number(parts[1])
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      throw new Error('address port must be an integer from 1 to 65535')
    }
  }

  private clearReadyTimer(): void {
    if (this.readyTimerId !== 0) {
      clearTimeout(this.readyTimerId)
      this.readyTimerId = 0
    }
  }

  private clearSendTimer(): void {
    if (this.sendTimerId !== 0) {
      clearTimeout(this.sendTimerId)
      this.sendTimerId = 0
    }
  }

  private terminate(reason: string): void {
    if (this.terminated) {
      return
    }
    this.terminated = true
    this.ready = false
    this.clearReadyTimer()
    this.clearSendTimer()
    if (this.expectingInsert || this.sendMayBeUnresolved || this.inFlightClientSeq !== 0) {
      this.observer.requireProcessRestart(
        `${reason}; SDK teardown skipped while dispatch, local insert, or SENDACK is unresolved`
      )
      return
    }
    if (this.listenersRegistered) {
      WKIM.shared.connectionManager().removeConnectStatusListener(this.connectStatusListener)
      WKIM.shared.messageManager().removeSendStatusListener(this.sendStatusListener)
      WKIM.shared.messageManager().removeNewMsgListener(this.newMessagesListener)
      this.listenersRegistered = false
    }
    WKIM.shared.connectionManager().disConnection(true)
    this.observer.requireProcessRestart(reason)
  }
}

/** Use only with brand-new acceptance accounts that have no history to recover. */
export function freshAccountConversationSource(uid: string): ConversationSource {
  return async (): Promise<WKSyncConversation> => {
    const result = new WKSyncConversation()
    result.uid = uid
    result.conversations = []
    return result
  }
}
