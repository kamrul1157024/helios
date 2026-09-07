package com.helios.helios

import android.app.Application
import com.pravera.flutter_foreground_task.FlutterForegroundTaskLifecycleListener
import com.pravera.flutter_foreground_task.FlutterForegroundTaskPlugin
import com.pravera.flutter_foreground_task.FlutterForegroundTaskStarter
import io.flutter.embedding.engine.FlutterEngine

/**
 * Attaches [HeliosNotificationsPlugin] to the foreground service's engine.
 *
 * The listener is registered here rather than from MainActivity because the
 * service can start into a process where no activity has ever run — the app was
 * swiped away and Android restarted the service alone. Application.onCreate is
 * the only hook both paths pass through.
 */
class HeliosApplication : Application() {
    private val taskLifecycleListener = object : FlutterForegroundTaskLifecycleListener {
        override fun onEngineCreate(flutterEngine: FlutterEngine?) {
            flutterEngine?.plugins?.add(HeliosNotificationsPlugin())
        }

        override fun onTaskStart(starter: FlutterForegroundTaskStarter) {}

        override fun onTaskRepeatEvent() {}

        override fun onTaskDestroy() {}

        override fun onEngineWillDestroy() {}
    }

    override fun onCreate() {
        super.onCreate()
        FlutterForegroundTaskPlugin.addTaskLifecycleListener(taskLifecycleListener)
    }
}
