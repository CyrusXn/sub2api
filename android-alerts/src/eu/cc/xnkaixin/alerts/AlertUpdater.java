package eu.cc.xnkaixin.alerts;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageInfo;
import android.content.pm.PackageInstaller;
import android.content.pm.PackageManager;
import android.os.Build;
import android.os.SystemClock;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Arrays;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

import javax.net.ssl.HttpsURLConnection;

final class AlertUpdater {
    static final long CHECK_INTERVAL = TimeUnit.MINUTES.toMillis(15);
    private static final long SUCCESS_INTERVAL = TimeUnit.HOURS.toMillis(6);
    private static final String REPO = "CyrusXn/sub2api";
    private static final String FEED = "https://raw.githubusercontent.com/" + REPO + "/xnkaixin/android-alerts/update.json";
    private static final String CHANNEL = "app_updates";
    private static final int NOTIFICATION_ID = 3;
    private static final int APK_LIMIT = 20 * 1024 * 1024;
    private static final ExecutorService WORKER = Executors.newSingleThreadExecutor();
    private static final AtomicBoolean BUSY = new AtomicBoolean();
    private static long nextCheck;
    private static volatile long installStarted;
    private static volatile int installing = -1;
    private static volatile Intent confirmation;

    static SharedPreferences prefs(Context context) {
        return context.getSharedPreferences("app_update", Context.MODE_PRIVATE);
    }

    static String versionName(Context context) {
        return installed(context, 0).versionName;
    }

    static Intent confirmation() {
        return confirmation;
    }

    static void check(Context source, boolean manual) {
        enqueue(source, manual, false);
    }

    static void retryInstall(Context source) {
        enqueue(source, true, true);
    }

    private static void enqueue(Context source, boolean manual, boolean retry) {
        if (!BUSY.compareAndSet(false, true)) return;
        Context context = source.getApplicationContext();
        WORKER.execute(() -> {
            try {
                long now = SystemClock.elapsedRealtime();
                if (!manual && now < nextCheck) return;
                if (installing != -1) {
                    if (!retry || now - installStarted < TimeUnit.MINUTES.toMillis(1)) return;
                    installing = -1;
                    confirmation = null;
                }
                nextCheck = now + CHECK_INTERVAL;
                runCheck(context, retry);
                nextCheck = now + SUCCESS_INTERVAL;
            } catch (Exception error) {
                status(context, "更新检查、下载或安装启动失败，请稍后重试");
            } finally {
                BUSY.set(false);
            }
        });
    }

    private static void runCheck(Context context, boolean retry) throws Exception {
        status(context, "正在检查更新…");
        ByteArrayOutputStream json = new ByteArrayOutputStream();
        fetch(new URL(FEED), json, 4096);
        JSONObject release = new JSONObject(json.toString(StandardCharsets.UTF_8.name()));
        int code = release.getInt("versionCode");
        String version = release.getString("versionName");
        String digest = release.getString("sha256");
        if (code <= 0 || !version.matches("[0-9]+\\.[0-9]+\\.[0-9]+") || !digest.matches("[a-f0-9]{64}")) {
            throw new IOException("无效的更新清单");
        }
        File apk = new File(context.getCacheDir(), "sub2api-update.apk");
        NotificationManager notifications = context.getSystemService(NotificationManager.class);
        if (code <= versionCode(installed(context, 0))) {
            apk.delete();
            notifications.cancel(NOTIFICATION_ID);
            status(context, "已是最新版本");
            return;
        }
        if (!apk.isFile() || !digest.equals(sha256(apk))) {
            status(context, "正在下载 v" + version + "…");
            File partial = new File(context.getCacheDir(), "sub2api-update.part");
            try {
                URL url = new URL("https://github.com/" + REPO + "/releases/download/android-alerts-"
                        + version + "/sub2api-alerts.apk");
                try (OutputStream out = new FileOutputStream(partial)) {
                    fetch(url, out, APK_LIMIT);
                }
                if (!digest.equals(sha256(partial)) || !partial.renameTo(apk)) {
                    throw new IOException("更新包校验或保存失败");
                }
            } finally {
                partial.delete();
            }
        }
        verifyApk(context, apk, code, version);
        if (!context.getPackageManager().canRequestPackageInstalls()) {
            show(context, "更新已下载，请点「授权应用内安装」继续");
        } else if (retry || prefs(context).getInt("attempted", 0) != code) {
            install(context, apk, code);
        } else {
            show(context, "更新已下载，请点「安装更新」继续");
        }
    }

    private static void verifyApk(Context context, File apk, int code, String version) throws IOException {
        int flags = Build.VERSION.SDK_INT >= 28 ? PackageManager.GET_SIGNING_CERTIFICATES : PackageManager.GET_SIGNATURES;
        PackageInfo current = installed(context, flags);
        PackageInfo next = context.getPackageManager().getPackageArchiveInfo(apk.getAbsolutePath(), flags);
        if (next == null || !current.packageName.equals(next.packageName) || !version.equals(next.versionName)
                || code != versionCode(next) || code <= versionCode(current)) throw new IOException("更新包信息不匹配");
        android.content.pm.Signature[] a = Build.VERSION.SDK_INT >= 28 && current.signingInfo != null
                ? current.signingInfo.getApkContentsSigners() : current.signatures;
        android.content.pm.Signature[] b = Build.VERSION.SDK_INT >= 28 && next.signingInfo != null
                ? next.signingInfo.getApkContentsSigners() : next.signatures;
        if (a == null || a.length != 1 || !Arrays.equals(a, b)) throw new IOException("更新包签名不匹配");
    }

    private static PackageInfo installed(Context context, int flags) {
        try {
            return context.getPackageManager().getPackageInfo(context.getPackageName(), flags);
        } catch (PackageManager.NameNotFoundException error) {
            throw new IllegalStateException(error);
        }
    }

    private static long versionCode(PackageInfo info) {
        return Build.VERSION.SDK_INT >= 28 ? info.getLongVersionCode() : info.versionCode;
    }

    private static void install(Context context, File apk, int version) throws IOException {
        PackageInstaller installer = context.getPackageManager().getPackageInstaller();
        // 进程若在等待确认时被回收，重试会重建自己的旧安装会话。
        for (PackageInstaller.SessionInfo session : installer.getMySessions()) {
            if (context.getPackageName().equals(session.getAppPackageName())) {
                installer.abandonSession(session.getSessionId());
            }
        }
        PackageInstaller.SessionParams params = new PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL);
        params.setAppPackageName(context.getPackageName());
        params.setSize(apk.length());
        if (Build.VERSION.SDK_INT >= 31) {
            params.setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED);
        }
        int id = installer.createSession(params);
        boolean committed = false;
        try (PackageInstaller.Session session = installer.openSession(id)) {
            try (InputStream in = new FileInputStream(apk); OutputStream out = session.openWrite("base.apk", 0, apk.length())) {
                byte[] buffer = new byte[8192];
                int count;
                while ((count = in.read(buffer)) != -1) out.write(buffer, 0, count);
                session.fsync(out);
            }
            Intent result = new Intent(context, UpdateReceiver.class);
            int flags = PendingIntent.FLAG_UPDATE_CURRENT;
            if (Build.VERSION.SDK_INT >= 31) flags |= PendingIntent.FLAG_MUTABLE;
            PendingIntent callback = PendingIntent.getBroadcast(context, id, result, flags);
            status(context, "正在安装更新…");
            installStarted = SystemClock.elapsedRealtime();
            installing = id;
            if (!prefs(context).edit().putInt("attempted", version).putInt("session", id).commit()) {
                throw new IOException("无法保存安装状态");
            }
            session.commit(callback.getIntentSender());
            committed = true;
        } finally {
            if (!committed) {
                installing = -1;
                prefs(context).edit().remove("session").remove("attempted").apply();
                installer.abandonSession(id);
            }
        }
    }

    static void result(Context context, Intent intent) {
        int id = intent.getIntExtra(PackageInstaller.EXTRA_SESSION_ID, -1);
        if (id == -1 || id != prefs(context).getInt("session", -1)) return;
        int result = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE);
        if (result == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            confirmation = intent.getParcelableExtra(Intent.EXTRA_INTENT);
            // 系统可能禁止后台拉起界面，由用户点击更新提醒进入确认页。
            show(context, "手机系统要求确认，请点「安装更新」继续");
            return;
        }
        confirmation = null;
        installing = -1;
        prefs(context).edit().remove("session").apply();
        if (result == PackageInstaller.STATUS_SUCCESS) {
            context.getSystemService(NotificationManager.class).cancel(NOTIFICATION_ID);
            status(context, "更新已安装");
        } else {
            show(context, "安装未完成，可点「安装更新」重试");
        }
    }

    private static void status(Context context, String text) {
        prefs(context).edit().putString("status", text).apply();
    }

    private static void show(Context context, String text) {
        status(context, text);
        NotificationManager manager = context.getSystemService(NotificationManager.class);
        manager.createNotificationChannel(new NotificationChannel(CHANNEL, "应用更新", NotificationManager.IMPORTANCE_DEFAULT));
        if (!manager.areNotificationsEnabled()) return;
        PendingIntent open = PendingIntent.getActivity(context, NOTIFICATION_ID, new Intent(context, MainActivity.class),
                PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        manager.notify(NOTIFICATION_ID, new Notification.Builder(context, CHANNEL)
                .setSmallIcon(R.drawable.ic_alert).setContentTitle("Sub2API 告警更新").setContentText(text)
                .setStyle(new Notification.BigTextStyle().bigText(text))
                .setContentIntent(open).setOnlyAlertOnce(true).setAutoCancel(true).build());
    }

    private static String sha256(File file) throws Exception {
        MessageDigest hash = MessageDigest.getInstance("SHA-256");
        try (InputStream in = new FileInputStream(file)) {
            byte[] buffer = new byte[8192];
            int count;
            while ((count = in.read(buffer)) != -1) hash.update(buffer, 0, count);
        }
        StringBuilder hex = new StringBuilder(64);
        for (byte value : hash.digest()) hex.append(Character.forDigit((value >>> 4) & 15, 16))
                .append(Character.forDigit(value & 15, 16));
        return hex.toString();
    }

    private static void fetch(URL url, OutputStream out, int limit) throws IOException {
        for (int hops = 0; hops < 4; hops++) {
            if (!"https".equals(url.getProtocol()) || url.getUserInfo() != null || (url.getPort() != -1 && url.getPort() != 443)
                    || !Arrays.asList("raw.githubusercontent.com", "github.com", "release-assets.githubusercontent.com",
                            "objects.githubusercontent.com").contains(url.getHost())) {
                throw new IOException("不受信任的更新地址");
            }
            HttpsURLConnection request = (HttpsURLConnection) url.openConnection();
            try {
                request.setInstanceFollowRedirects(false);
                request.setConnectTimeout(10_000);
                request.setReadTimeout(15_000);
                int code = request.getResponseCode();
                if (code == 301 || code == 302 || code == 303 || code == 307 || code == 308) {
                    String location = request.getHeaderField("Location");
                    if (location == null) throw new IOException("更新地址重定向无效");
                    url = new URL(url, location);
                    continue;
                }
                if (code != 200 || request.getContentLengthLong() > limit) throw new IOException("更新请求失败");
                try (InputStream in = request.getInputStream()) {
                    byte[] buffer = new byte[8192];
                    int count;
                    int total = 0;
                    while ((count = in.read(buffer)) != -1) {
                        total += count;
                        if (total > limit) throw new IOException("更新包超出大小限制");
                        out.write(buffer, 0, count);
                    }
                }
                return;
            } finally {
                request.disconnect();
            }
        }
        throw new IOException("更新地址重定向次数过多");
    }
}
