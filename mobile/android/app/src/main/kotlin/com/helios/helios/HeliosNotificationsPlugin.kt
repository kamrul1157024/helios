package com.helios.helios

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.media.AudioAttributes
import android.media.RingtoneManager
import android.os.Build
import android.os.VibrationEffect
import android.os.Vibrator
import android.os.VibratorManager
import io.flutter.embedding.engine.plugins.FlutterPlugin
import io.flutter.plugin.common.MethodChannel

/**
 * Channel creation and the manual sound/vibration path.
 *
 * A plugin rather than a block inside MainActivity because the foreground
 * service runs Dart on its own FlutterEngine. Registered on the activity engine
 * only, this channel would not exist while the app is closed, and every
 * notification the background watcher raised on a ColorOS device would be
 * silent — which is the one case the service exists for.
 */
class HeliosNotificationsPlugin : FlutterPlugin {
    private var channel: MethodChannel? = null
    private lateinit var context: Context

    override fun onAttachedToEngine(binding: FlutterPlugin.FlutterPluginBinding) {
        context = binding.applicationContext
        channel = MethodChannel(binding.binaryMessenger, CHANNEL).apply {
            setMethodCallHandler { call, result ->
                when (call.method) {
                    "createChannels" -> {
                        createNotificationChannels(call.argument("channels"))
                        result.success(null)
                    }
                    "playNotificationSound" -> {
                        val sound = call.argument<Boolean>("sound") ?: true
                        val vibration = call.argument<Boolean>("vibration") ?: true
                        playNotificationSound(sound, vibration)
                        result.success(null)
                    }
                    else -> result.notImplemented()
                }
            }
        }
    }

    override fun onDetachedFromEngine(binding: FlutterPlugin.FlutterPluginBinding) {
        channel?.setMethodCallHandler(null)
        channel = null
    }

    private fun createNotificationChannels(channels: List<Map<String, Any>>?) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        channels?.forEach { ch ->
            val id = ch["id"] as String
            val name = ch["name"] as String
            val desc = ch["description"] as? String ?: ""
            val importance = (ch["importance"] as? Int) ?: NotificationManager.IMPORTANCE_HIGH

            manager.deleteNotificationChannel(id)

            // Sound and vibration are disabled on the channel because we play
            // them manually via playNotificationSound() on the alarm stream.
            // This avoids double-sound on devices where channel sound works fine.
            val channel = NotificationChannel(id, name, importance).apply {
                description = desc
                enableVibration(false)
                setSound(null, null)
                enableLights(true)
                setShowBadge(true)
                setBypassDnd(true)
            }

            manager.createNotificationChannel(channel)
        }
    }

    /**
     * Play the default notification sound and vibrate directly,
     * bypassing notification channel sound settings which some OEMs
     * (Realme/OPPO/ColorOS) strip on channel creation.
     */
    private fun playNotificationSound(sound: Boolean, vibration: Boolean) {
        if (sound) {
            try {
                val uri = RingtoneManager.getDefaultUri(RingtoneManager.TYPE_NOTIFICATION)
                val ringtone = RingtoneManager.getRingtone(context, uri)
                if (ringtone != null) {
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                        ringtone.audioAttributes = AudioAttributes.Builder()
                            .setUsage(AudioAttributes.USAGE_ALARM)
                            .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                            .build()
                    } else {
                        @Suppress("DEPRECATION")
                        ringtone.streamType = android.media.AudioManager.STREAM_ALARM
                    }
                    ringtone.play()
                }
            } catch (_: Exception) {}
        }

        if (vibration) {
            try {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                    val vibratorManager =
                        context.getSystemService(Context.VIBRATOR_MANAGER_SERVICE) as VibratorManager
                    val vibrator = vibratorManager.defaultVibrator
                    vibrator.vibrate(VibrationEffect.createWaveform(longArrayOf(0, 500, 200, 500), -1))
                } else {
                    @Suppress("DEPRECATION")
                    val vibrator = context.getSystemService(Context.VIBRATOR_SERVICE) as Vibrator
                    vibrator.vibrate(VibrationEffect.createWaveform(longArrayOf(0, 500, 200, 500), -1))
                }
            } catch (_: Exception) {}
        }
    }

    companion object {
        private const val CHANNEL = "com.helios.helios/notifications"
    }
}
