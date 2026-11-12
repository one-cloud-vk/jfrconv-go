import java.nio.file.Path;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import jdk.jfr.consumer.RecordedEvent;
import jdk.jfr.consumer.RecordingFile;

public class SpanInspect {
    public static void main(String[] args) throws Exception {
        long samples = 0;
        try (RecordingFile file = new RecordingFile(Path.of(args[0]))) {
            while (file.hasMoreEvents()) {
                RecordedEvent event = file.readEvent();
                String name = event.getEventType().getName();
                if (name.equals("profiler.Span")) {
                    String tag = event.getString("tag");
                    String encoded = tag == null ? "-" : Base64.getEncoder().encodeToString(tag.getBytes(StandardCharsets.UTF_8));
                    System.out.println("SPAN\t" + encoded + "\t" + event.getDuration().toNanos());
                } else if (name.equals("jdk.ExecutionSample") || name.equals("jdk.NativeMethodSample") || name.equals("jdk.CPUTimeSample")) {
                    samples++;
                }
            }
        }
        System.out.println("SAMPLES\t" + samples);
    }
}
