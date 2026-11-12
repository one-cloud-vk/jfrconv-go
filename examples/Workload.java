import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.time.Duration;
import java.util.zip.Deflater;
import jdk.jfr.Recording;

public class Workload {
    private static volatile int sink;

    public static void main(String[] args) throws Exception {
        try (Recording recording = new Recording()) {
            recording.enable("jdk.ExecutionSample").withPeriod(Duration.ofMillis(1));
            recording.enable("jdk.NativeMethodSample").withPeriod(Duration.ofMillis(1));
            recording.start();
            long end = System.nanoTime() + Duration.ofSeconds(5).toNanos();
            while (System.nanoTime() < end) {
                handleRequest("GET /orders?customer=42 HTTP/1.1\r\nHost: localhost\r\n");
            }
            recording.stop();
            recording.dump(Path.of(args.length == 0 ? "example.jfr" : args[0]));
        }
        System.out.println(sink);
    }

    private static void handleRequest(String request) throws Exception {
        String[] headers = parseHeaders(request);
        byte[] body = renderOrders(headers[0]);
        signResponse(body);
        compressResponse(body);
    }

    private static String[] parseHeaders(String request) {
        return request.split("\\r\\n");
    }

    private static byte[] renderOrders(String customer) {
        StringBuilder body = new StringBuilder(8192);
        for (int i = 0; i < 150; i++) {
            body.append("{\"customer\":\"").append(customer)
                .append("\",\"order\":").append(i).append("}\n");
        }
        return body.toString().getBytes(StandardCharsets.UTF_8);
    }

    private static void signResponse(byte[] body) throws Exception {
        MessageDigest sha = MessageDigest.getInstance("SHA-256");
        for (int i = 0; i < 12; i++) sha.update(body);
        sink ^= sha.digest()[0];
    }

    private static void compressResponse(byte[] body) {
        Deflater deflater = new Deflater();
        try {
            deflater.setInput(body);
            deflater.finish();
            byte[] output = new byte[body.length];
            sink ^= deflater.deflate(output);
        } finally {
            deflater.end();
        }
    }
}
