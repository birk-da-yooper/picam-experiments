package main

import (
    "bytes"
    "fmt"
    "os/exec"
 //   "io"
    "os"
    "math"
    "strconv"
//    "strings"
    "image"
    "image/jpeg"
    "image/draw"
    "image/color"
    "errors"
    "time"
)

var debug          = false
var working_dir    = "/tmp"
var images_dir     = "/pub/images/"

var width_test   = int(500)
var height_test  = int(500)
var width_full   = int(2952)
var height_full  = int(1944)

//var camera_settings = []string{"-ex", "night", "-awb", "auto"}
// rotate 180 so that we sit the R Pi down on it's flat size, not the USB side
//var camera_settings = []string{"-ex", "auto", "-awb", "auto", "--nopreview", "--rotation", "180"}
var camera_settings = []string{"-ex", "auto", "-awb", "auto", "--nopreview"}

var color_depth           = uint(16)    // bits of depth

// old method
var o_min_color_change    = float64(15) // percent of color change, in any given channel, to indicate a changed pixel
var o_sensitivity         = uint32(0)   // value to use for the compare

type color_sensitivity struct {

    red_pct, green_pct, blue_pct float64
    red, green, blue uint32
}

var sensitivity = color_sensitivity{red_pct:   100.0,
                                    green_pct: 15.0,
                                    blue_pct:  100.0,
                                    red:       0,
                                    green:     0,
                                    blue:      0,
                                   }

var min_pix_change      = float32(1.5) // percent of picture change to trigger change detection
var detection_threshold = int(0)       // min change expressed as pixel count, will get set in the body

var scan_speed = int(2)  // 1 =every pixel, 2=every other, etc.  higher values are faster, but are less sensitive to change
var counter_test_images = int(0)
var counter_dbg_images  = int(0)

var error_count = int(0)

var camera_activated    = 1;   // is the camera turned on, or in idle mode
var inactive_loop_delay = 10;   // delay in seconds for idle loop when in active

// channels for accepting query/action from web side and tp share settings info
// both are bufferred for a single item
var web_interface  = make(chan string, 1)

func set_sensitivity(to_set *color_sensitivity) () {

    depth := 1 << color_depth
    to_set.red   = uint32(math.Floor((to_set.red_pct/100)   * float64(depth)))
    to_set.green = uint32(math.Floor((to_set.green_pct/100) * float64(depth)))
    to_set.blue  = uint32(math.Floor((to_set.blue_pct/100)  * float64(depth)))
}

func adjust_sensitivity(to_set *color_sensitivity, red, green, blue float64) () {

    to_set.red_pct   = red
    to_set.green_pct = green
    to_set.blue_pct  = blue

    set_sensitivity(to_set)
}

func main () {

    // check for command line flags here
    // check for config file here, and implement if found
//width_test   = width_full
//height_test  = height_full

    adjust_sensitivity(&sensitivity, 100.0, 15.0, 100.0)

//    depth := 1 << color_depth
//    o_sensitivity =  uint32(math.Floor((o_min_color_change/100) * float64(depth)))

    pix_count := float32(width_test * height_test)
    detection_threshold = int(pix_count * ( min_pix_change / 100 ))/scan_speed

    if debug == true {

        fmt.Printf("color sensitivity: %v\n", sensitivity)
        fmt.Printf("motion detection threshold %v%% [%v:%v]\n", min_pix_change, detection_threshold, pix_count)
    }

    //start out by capturing initial conditions
    //then during regular loop check to see if they've toggled

    prev_image := image.NewRGBA(image.Rect(0, 0, width_test, height_test))   // or we could load from a starter

    // indefinite loop, until explicit escape
    for ;; {

        if error_count > 25 {

            fmt.Printf("Too many errors to continue [count=%v]\n", error_count)
            camera_activated = 0
            error_count = 0
            // return
        }

        if camera_activated == 0 {

            time.Sleep(time.Duration(inactive_loop_delay))
            continue
        }

        test_image, fout, err := capture_test_image()

        // save the first image we get
        if counter_test_images == 0 {

            prev_image = test_image

            // we should first check to see if the user wants to retain this first image or not

            // create a new file using the image that we loaded
            //save_test_image("", test_image)

            // just rename the capture file
            retain_image(fout, "")
            continue
        }

        if ( debug == true ) && ( err != nil ) {

            fmt.Println("\nfile name returned from capture_image:")
            fmt.Print(fout)
            fmt.Println("\nerrs info:")
            fmt.Print(err)
            fmt.Println()
        }

        // compare test image against previous image
        // if at least X pixels differ by at least y% a boolean true is returned

        differs := compare_images(prev_image, test_image)

        if differs == true {

            fmt.Println("\ndiffers flag indicates motion was detected")

            //# we can either rename the image used to detect motion if it's large enough
            retain_image(fout, "")

            //# (or save it out from memory to a new file)
            //save_test_image("", test_image)

            //# or we could take a new full sized picture if using a small image for quick sampling rates
            //test_image, fout, err := capture_full_image()

            // rotate the current sample into place as the new baseline
            prev_image = test_image

            if err != nil {

                fmt.Print(err)

            } else {

                // reset the comparison baseline
                // NOTE:  make this a toogled behavior, some users may want to always compare to the first image taken
                //        or a known good image that was previously taken

                // we can simply copy the pointer, no need to use a scan + draw to copy it thank goodness
                prev_image = test_image
            }

        } else if debug == true {

            //fmt.Println("\ndiffers is false")
            fmt.Print("-")
        }
    }

    // never gets here actually
    return
}

// implement command sent to us as a string
func do_it (cmd_line string, args_in []string) (string, string, error) {

    // buffer to capture the output
    var stdout bytes.Buffer
    var stderr bytes.Buffer

    // twhen using ARgs with exec.Cmd arg 0 must be the command itself
    // note the ellipsis allows us to  pass in the slice args_in directly instead of requiring it to be individual strings
    args_to_use := []string{cmd_line}
    args_to_use  = append(args_to_use, args_in...)

    //fmt.Printf("args_to_use type %t and holds %v\n\n", args_to_use, args_to_use)

    // create the command
                             //Args:   []string{},
    var handle  = &exec.Cmd{ Path:   cmd_line,
                             Args:   args_to_use,
                             Dir:    working_dir,
                           }

    // send the stdout to our buffer
    handle.Stdout = &stdout
    handle.Stderr = &stderr

    // we don't need to send any stdin to the proces
    //pipe, _ := handle.StdinPipe()
    //fmt.Fprint(pipe, "/\n")

    // ready to go, let's do it now
    err := handle.Run()

    return stdout.String(), stderr.String(), err
}

func capture_test_image() (*image.RGBA, string, error) {

   return capture_image(width_test, height_test, "", "")
}

// for saving an image that has been modified, or if we want the original file to be retained
func save_test_image(filename string, img_to_save *image.RGBA) error  {

    if filename == "" {

        counter_dbg_images++
        //filename = images_dir + "test_img_" + strconv.Itoa(counter_dbg_images) + ".jpg"
        filename = images_dir + "test_img_" + get_timestamp() + ".jpg"
    }

    save_to, err := os.Create(filename)
    defer save_to.Close()

    jpeg.Encode(save_to, img_to_save, &jpeg.Options{jpeg.DefaultQuality})

    if err != nil {

        error_count++
    }

    return err
}

// for speed and efficiency we can just rename our captured sample if it's already of sufficient size
func retain_image(current_filename string, final_name string) (string, error)  {

    counter_test_images++

    if current_filename == "" {

        err := errors.New("missing current filename")
        return "missing current filename", err
    }

    if final_name == "" {

        // currently ignoring the provided name and creating a name by convention
        //final_name = images_dir + "test_img_" + strconv.Itoa(counter_test_images) + ".jpg"
        final_name = images_dir + "test_img_" + get_timestamp() + ".jpg"
    }

    command := "/bin/mv"
    args_list := []string{current_filename, final_name}

    _, err_msg, err := do_it(command, args_list)

    return  err_msg, err
}

func capture_full_image() (*image.RGBA, string, error) {

   filename := "output.jpg"    // update to use standardized naming, with timestamp

   return capture_image(width_full, height_full, filename, "")
}

// capture an image of specified size using desired options
// return as pointer to an image array for overall speed and efficiency
func capture_image (width int, height int, output_file string, settings string) (*image.RGBA, string, error) {

    if width == 0 {

        width = width_test
    }

    if height == 0 {

        height = height_test
    }

    if settings == "" {

         settings = ""
    }

    if output_file == "" {

        output_file = images_dir + "output.jpg"    // default, will get overwritten frequently
    }

    //newimage := new(image.RGBA)    // create as pointer
    newimage := image.NewRGBA(image.Rect(0, 0, width, height))   // or is this
    timeout := 100     //  time in ms before it takes the picture and exits oout

    // note the -o - means  the output is sent back to stdout, so we catch it via stdin here
    //command := "/usr/bin/raspistill %s -w %s -h %s %s -e jpg -n -o -" % (settings, width, height, timeout)
    command := "/usr/bin/raspistill"

    // normally for a  command we need to specify the command in index 0 but the do_it function does that for us
    args_list := []string{}
    args_list  = append(args_list, camera_settings...)

// this here is ugly as sin, but appears to work...

    args_list  = append(args_list, []string{"-w",  strconv.Itoa(width),
                                            "-h",  strconv.Itoa(height),
                                            "-t",  strconv.Itoa(timeout),
                                            "-e", "jpg",
                                            "-n",
                                            "-o", output_file}...)

    _, err_info, err := do_it(command, args_list)

    if ( err != nil ) {

        error_count++
        fmt.Printf("Error occurred while executing command: %v\n", command)
        fmt.Print(err)
        fmt.Println()
        fmt.Print(err_info)
        fmt.Println()

        return newimage, output_file, err
    }

    newimage, err = load_image(output_file, width, height)

    return newimage, output_file, err

/* params for raspistill

Runs camera for specific time, and take JPG capture at end if requested
usage: raspistill [options]
Image parameter commands

-?, --help  : This help information
-w, --width : Set image width <size>
-h, --height    : Set image height <size>
-q, --quality   : Set jpeg quality <0 to 100>
-r, --raw   : Add raw bayer data to jpeg metadata
-o, --output    : Output filename <filename> (to write to stdout, use '-o -'). If not specified, no file is saved
-l, --latest    : Link latest complete image to filename <filename>
-v, --verbose   : Output verbose information during run
-t, --timeout   : Time (in ms) before takes picture and shuts down (if not specified, set to 5s)
-th, --thumb    : Set thumbnail parameters (x:y:quality) or none
-d, --demo  : Run a demo mode (cycle through range of camera options, no capture)
-e, --encoding  : Encoding to use for output file (jpg, bmp, gif, png)
-x, --exif  : EXIF tag to apply to captures (format as 'key=value') or none
-tl, --timelapse    : Timelapse mode. Takes a picture every <t>ms
-fp, --fullpreview  : Run the preview using the still capture resolution (may reduce preview fps)
-k, --keypress  : Wait between captures for a ENTER, X then ENTER to exit
-s, --signal    : Wait between captures for a SIGUSR1 from another process
-g, --gl    : Draw preview to texture instead of using video render component
-gc, --glcapture    : Capture the GL frame-buffer instead of the camera image

Preview parameter commands

-p, --preview   : Preview window settings <'x,y,w,h'>
-f, --fullscreen    : Fullscreen preview mode
-op, --opacity  : Preview window opacity (0-255)
-n, --nopreview : Do not display a preview window

Image parameter commands

-sh, --sharpness    : Set image sharpness (-100 to 100)
-co, --contrast : Set image contrast (-100 to 100)
-br, --brightness   : Set image brightness (0 to 100)
-sa, --saturation   : Set image saturation (-100 to 100)
-ISO, --ISO : Set capture ISO
-vs, --vstab    : Turn on video stabilisation
-ev, --ev   : Set EV compensation
-ex, --exposure : Set exposure mode (see Notes)
-awb, --awb : Set AWB mode (see Notes)
-ifx, --imxfx   : Set image effect (see Notes)
-cfx, --colfx   : Set colour effect (U:V)
-mm, --metering : Set metering mode (see Notes)
-rot, --rotation    : Set image rotation (0-359)
-hf, --hflip    : Set horizontal flip
-vf, --vflip    : Set vertical flip
-roi, --roi : Set region of interest (x,y,w,d as normalised coordinates [0.0-1.0])
-ss, --shutter  : Set shutter speed in microseconds
-awbg, --awbgains   : Set AWB gains - AWB mode must be off


Notes

Exposure mode options :
auto,night,nightpreview,backlight,spotlight,sports,snow,beach,verylong,fixedfps,antishake,fireworks

AWB mode options :
off,auto,sun,cloud,shade,tungsten,fluorescent,incandescent,flash,horizon

Image Effect mode options :
none,negative,solarise,sketch,denoise,emboss,oilpaint,hatch,gpen,pastel,watercolour,film,blur,saturation,colourswap,washedout,posterise,colourpoint,colourbalance,cartoon

Metering Mode options :
average,spot,backlit,matrix

Preview parameter commands

-gs, --glscene  : GL scene square,teapot,mirror,yuv,sobel
-gw, --glwin    : GL window settings <'x,y,w,h'>

*/

}

func load_image (filename string, width int, height int)  (*image.RGBA, error) {

    //img := new(image.RGBA) // create as pointer
    img := image.NewRGBA(image.Rect(0, 0, width, height))

    readit, err := os.Open(filename)
    defer readit.Close()

    if err != nil {

        error_count++
        return img, err
    }

    //img, err = jpeg.Decode(readit)
    dec, _ := jpeg.Decode(readit)

    draw.Draw(img, img.Bounds(), dec, image.Point{0,0}, draw.Src)

    return img, err
}

func compare_images (baseline *image.RGBA, test_image *image.RGBA) (bool) {

     differs_flag := false      // will confirm there is a difference

    /*
     when scanning we should take an intelligent approach...

     ... generally changes will be something entering the scene so scanning from outside borders
     and spiralling in may make sense for a 'big picture' approach

     ... but when watching only select areas (eg config'd to watch only some doors and windows) then
     a simple top->bottom, left->right approach is probably fine.  Note that a good design would give
     each designated watch area a prioritization value

     ... an advanced approach would be to use adapative scanning that tracks which pixels show change
     the most frequently and check those first.  by using a rolling avg we should be able to adjust
     for different areas as time goes by to keep things optimally quick.  another feature that could
     incorporate this info is to filter out areas that start showing too much regular change, eg leaves
     and weeds blowing in a wind that has come up

     .... also considering doing a distribution analysis of how much pixels have changed when they're
     triggered.  A quick guassian distribution analysis to determine if the color sensitivy needs to be adjusted
     may be useful.

     ... another advanced approach would be to look at surrounding pixels when a changed one is found,
     hopefully finding significant/triggering changes more quickly.  We can also then identify "objects"
     by size/position and untrack them

     check for G R B using default threshholds, if defaults are 'zero' then skip that test
     generally checking for just green should be sufficient since green is the most sensitive

    */

    save_image := image.NewRGBA(image.Rect(0, 0, width_test, height_test))   // for debugging, but could be used for motion highlighting

    //bbi := baseline.Bounds()    // bounds for baseline image, in case we have a baseline image of different size than test images
    bti := test_image.Bounds()  // bounds for test comparison image (area doesn't have to be whole baseline image)

    // if the test image has no size, we want to flag it for attention
    if ( bti.Min.X == bti.Max.X ) || ( bti.Min.Y == bti.Max.Y ) {

        differs_flag = true
    }

    depth   := uint32(1 << color_depth)     // may want this as global for efficiency
    detected := int(0)

    // scan from outside edges in, going from top to bottom
    // the idea is that new motion is most likely items entering the field of vision

    for x := bti.Min.X; x <= (int(bti.Max.X/2) + 0); x += scan_speed {

        if ( differs_flag == true ) && ( debug == false ) {

            break
        }

        for y := bti.Min.Y; y < bti.Max.Y; y += scan_speed {

            tripped := 0
            l := bti.Min.X + x
            r := bti.Max.X - x

            pixel_base := baseline.At(l, y)
            pixel_test := test_image.At(l, y)

            rb, gb, bb,_   := pixel_base.RGBA()
            rt, gt, bt, at := pixel_test.RGBA()

            // check green, red, blue in order to see if any trip it as this is the anticipated order of color sensitivity
            if (( sensitivity.green_pct < 100.0                            )  &&
                ((( (gb - gt) < depth ) && ( (gb - gt) > sensitivity.green )) ||
                 (( (gt - gb) < depth ) && ( (gt - gb) > sensitivity.green )))) {

/*
   while still working with this relatively trivial approach to detection keep the following in mind
   it probably is much faster to do bitwise checks....
   EG if min change is 10% then check bits 7-4 first, because at least one of those MUST flib to exceeed 10%
      better yet check 7-5 first, then 4-0
      ( B ^ T ) >> bitsensitivity
1 1 1 1 0 0 1
1 1 1 1 0 0 1
0

1 0 1 1 0 0 1
1 1 1 0 0 0 1
0 1 0 1 0 0 0 = shift over two = 0 0 0 1 0 1 0

BASE   7   6   5   4   3   2   1   0
TEST   7   6   5   4   3   2   1   0
                                   1/255 :  0.4
                                2/255    :  0.8
                           4/255         :  1.6
                        8/255            :  3.1
                   16/255                :  6.3
               32/255                    : 12.5
           64/255                        : 25.1
      128/255                            : 50.2

*/
                tripped++
            }

            if (( sensitivity.red_pct < 100.0                            )  &&
                ((( (rb - rt) < depth ) && ( (rb - rt) > sensitivity.red )) ||
                 (( (rt - rb) < depth ) && ( (rt - rb) > sensitivity.red )))) {

                tripped++
            }

            if (( sensitivity.blue_pct < 100.0                             )  &&
                ((( (bb - bt) < depth ) && ( (bb - bt) > sensitivity.blue )) ||
                 (( (bt - bb) < depth ) && ( (bt - bb) > sensitivity.blue )))) {

                tripped++
            }

            // first put here as framework to support an adaptive detection methodology
            // however, we could use it to keep detecting on the same side of the image
            // until we have exceeded the detect threshold or see no more changes on this side

            if tripped > 0 {

                detected++
                tripped = 0

                // bring up low change bits... but just makes things weird
                if debug == true {

                    gt = depth - 1
                    rt = 0
                    bt = 0
                }

            } else {

                gt = gb
                rt = rb
                bt = bb
             }

            save_image.Set(l, y, color.RGBA64{uint16(rt), uint16(gt), uint16(bt), uint16(at)})

            // ensure we don't process the same column twice in the middle
            if l == r {

                continue
            }

            // now compare each of the channels desired...

            pixel_base = baseline.At(r, y)
            pixel_test = test_image.At(r, y)

            rb, gb, bb, _  = pixel_base.RGBA()
            rt, gt, bt, at = pixel_test.RGBA()

            if (( sensitivity.green_pct < 100.0                            )  &&
                ((( (gb - gt) < depth ) && ( (gb - gt) > sensitivity.green )) ||
                 (( (gt - gb) < depth ) && ( (gt - gb) > sensitivity.green )))) {

                tripped++
            }

            if (( sensitivity.red_pct < 100.0                            )  &&
                ((( (rb - rt) < depth ) && ( (rb - rt) > sensitivity.red )) ||
                 (( (rt - rb) < depth ) && ( (rt - rb) > sensitivity.red )))) {

                tripped++
            }

            if (( sensitivity.blue_pct < 100.0                             )  &&
                ((( (bb - bt) < depth ) && ( (bb - bt) > sensitivity.blue )) ||
                 (( (bt - bb) < depth ) && ( (bt - bb) > sensitivity.blue )))) {

                tripped++
            }

            if tripped > 0 {

                detected++
                tripped = 0

                // bring up low change bits... but just makes things weird
                if debug == true {

                    // highlight all changes in white
                    gt = depth - 1
                    rt = 0
                    bt = 0
                }

            } else {

                gt = gb
                rt = rb
                bt = bb
            }

            save_image.Set(r, y, color.RGBA64{uint16(rt), uint16(gt), uint16(bt), uint16(at)})

            if detected >= detection_threshold {

                differs_flag = true
            }

            if ( differs_flag == true ) { break}

            if ( differs_flag == true ) && ( debug == false ) {

                break
            }
        }
    }

    if debug == true {

        pix_count := width_test * height_test
        pct := (float64(detected) / float64(pix_count)) * 1000
        pct  = math.Floor(pct) / 10

        if differs_flag == true {

        fmt.Printf("detected change amount [%v" + "%%" + "]  pixels changed:threshold:total[%v:%v:%v]\n", pct, detected, detection_threshold, pix_count)

            counter_dbg_images++
            filename := images_dir + "debug_img_" + strconv.Itoa(counter_dbg_images) + ".jpg"

            e2 := save_test_image(filename, save_image)

            if e2 != nil {

                fmt.Println("error while saving debugging image where changes are highlighted\n")
                fmt.Print(e2)
            }
        }
    }

    return differs_flag
}

func get_timestamp () string {

    year, month, date    := time.Now().Date()
    hour, minute, second := time.Now().Clock()

    // year should be four digits
    timestamp := strconv.Itoa(year)

    if int(month) < 10 {

        timestamp += "0" + strconv.Itoa(int(month))

    } else {

        timestamp += strconv.Itoa(int(month))
    }

    if date < 0 {

        timestamp += "0" + strconv.Itoa(date)

    } else {

        timestamp += strconv.Itoa(date)
    }

    timestamp += "-"

    if hour < 10 {

        timestamp += "0" + strconv.Itoa(hour)

    } else {

        timestamp += strconv.Itoa(hour)
    }

    if minute < 10 {

        timestamp += "0" + strconv.Itoa(minute)

    } else {

        timestamp += strconv.Itoa(minute)
    }

    if second < 0 {

        timestamp += "0" + strconv.Itoa(second)

    } else {

        timestamp += strconv.Itoa(second)
    }

    return timestamp
}
